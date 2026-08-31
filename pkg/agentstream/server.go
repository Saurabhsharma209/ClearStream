// Package agentstream (this file) implements the missing half of the
// package: a real WebSocket server that speaks Exotel AgentStream's wire
// protocol (connected/start/media/dtmf/stop/mark/clear, plus the
// ClearStream-defined "reconfigure" control event) and drives a per-call
// pkg/audio.Pipeline built entirely from that call's CustomParameters.
//
// Nothing here is hardcoded per customer or call: every DSP decision --
// which suppressor backend, whether VAD/AGC/adaptive tiering are on, and
// their thresholds -- is read from data in the StartEvent (or changed live
// via a ReconfigureEvent), never from a fixed switch/case tied to an
// account. See docs/exotel-agentstream-integration.md for the exact
// CustomParameters keys this server understands and example requests.
package agentstream

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ServerConfig configures an AgentStreamServer.
type ServerConfig struct {
	// Logger is an optional zap logger. If nil, a no-op logger is used.
	Logger *zap.Logger

	// DefaultBackend is the noise-suppression backend used when a call's
	// start event does not specify one via CustomParameters["ns_model"].
	// Falls back to "passthrough" if empty.
	DefaultBackend string

	// MaxFrameBytes bounds a single incoming WebSocket message. Default: 64 KiB.
	MaxFrameBytes int64
}

// AgentStreamServer terminates Exotel AgentStream's WSS protocol and runs
// each stream's audio through a per-call ClearStream Pipeline.
type AgentStreamServer struct {
	cfg      ServerConfig
	logger   *zap.Logger
	upgrader websocket.Upgrader
}

// NewAgentStreamServer creates a new AgentStreamServer.
func NewAgentStreamServer(cfg ServerConfig) *AgentStreamServer {
	if cfg.MaxFrameBytes <= 0 {
		cfg.MaxFrameBytes = 65536
	}
	if cfg.DefaultBackend == "" {
		cfg.DefaultBackend = "passthrough"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AgentStreamServer{
		cfg:    cfg,
		logger: logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  int(cfg.MaxFrameBytes),
			WriteBufferSize: int(cfg.MaxFrameBytes),
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// Handler returns an http.Handler that upgrades to WebSocket and speaks the
// AgentStream protocol for the lifetime of the connection.
func (s *AgentStreamServer) Handler() http.Handler {
	return http.HandlerFunc(s.serveWS)
}

// callState holds everything scoped to a single stream/call.
type callState struct {
	mu            sync.Mutex
	pipeline      *audio.Pipeline
	state         StreamState
	streamSID     string
	callSID       string
	sampleRate    int
	seq           uint64
	agcEnabled    bool
	tieredEnabled bool
}

func (s *AgentStreamServer) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn("agentstream: upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	call := &callState{state: StateCreated}
	conn.SetReadLimit(s.cfg.MaxFrameBytes)

	if err := s.send(conn, ConnectedEvent{Event: EventConnected}); err != nil {
		s.logger.Warn("agentstream: failed to send connected event", zap.Error(err))
		return
	}

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			s.logger.Info("agentstream: connection closed", zap.Error(err))
			break
		}
		if msgType != websocket.TextMessage {
			// Exotel's AgentStream protocol is JSON text frames; anything
			// else on this leg is unexpected (this is not the raw-binary-PCM
			// bridge in pkg/websocket -- that is a separate, different
			// integration point).
			s.logger.Warn("agentstream: ignoring non-text frame", zap.Int("type", msgType))
			continue
		}
		if err := s.handleMessage(conn, call, data); err != nil {
			s.logger.Error("agentstream: message handling error", zap.Error(err))
			_ = s.send(conn, ErrorEvent{
				Event:     EventError,
				StreamSID: call.streamSID,
				Code:      FailureCodeInternal,
				Message:   err.Error(),
			})
		}
	}

	call.mu.Lock()
	if call.pipeline != nil {
		call.pipeline.Close()
	}
	call.mu.Unlock()
}

// envelope is used only to sniff the "event" discriminator before decoding
// into the concrete event struct.
type envelope struct {
	Event EventType `json:"event"`
}

func (s *AgentStreamServer) handleMessage(conn *websocket.Conn, call *callState, data []byte) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}

	switch env.Event {
	case EventStart:
		var ev StartEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("decode start event: %w", err)
		}
		return s.handleStart(call, &ev)
	case EventMedia:
		var ev MediaEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("decode media event: %w", err)
		}
		return s.handleMedia(conn, call, &ev)
	case EventDTMF:
		// DTMF is informational for the enhancement pipeline; not acted on
		// here. A bot that needs digits reads them from its own leg.
		return nil
	case EventClear:
		// No queued playback buffer exists on this leg; accepted for
		// protocol completeness only.
		return nil
	case EventMark:
		return nil
	case EventReconfigure:
		var ev ReconfigureEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("decode reconfigure event: %w", err)
		}
		return s.handleReconfigure(call, &ev)
	case EventStop:
		var ev StopEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("decode stop event: %w", err)
		}
		return s.handleStop(call, &ev)
	default:
		s.logger.Warn("agentstream: unhandled event type", zap.String("event", string(env.Event)))
		return nil
	}
}

// handleStart builds this call's Pipeline entirely from CustomParameters --
// no per-customer/per-call branching in code, only key lookups with safe
// defaults. Recognised keys (all optional):
//
//	ns_model             backend: rnnoise | rnnoise-onnx | deepfilter | deepfilter-server | passthrough
//	ns_model_path        ModelPath, required for deepfilter/rnnoise-onnx
//	ns_aggressiveness    0-3
//	ns_vad               "static" or "adaptive" to enable VAD
//	ns_agc               "true" to enable AGC
//	ns_agc_target_rms    override AGC target RMS
//	ns_mode              "adaptive" to enable TieredNR
//	ns_high_snr_db       override TieredNR.HighSNRThreshold
//	ns_low_snr_db        override TieredNR.LowSNRThreshold
func (s *AgentStreamServer) handleStart(call *callState, ev *StartEvent) error {
	call.mu.Lock()
	defer call.mu.Unlock()

	call.streamSID = ev.StreamSID
	call.callSID = ev.CallSID
	call.sampleRate = ev.SampleRate
	if call.sampleRate == 0 {
		call.sampleRate = 8000
	}

	params := ev.CustomParameters
	if params == nil {
		params = CustomParameters{}
	}

	suppCfg := model.SuppressorConfig{
		Backend:        firstNonEmpty(params["ns_model"], s.cfg.DefaultBackend),
		ModelPath:      params["ns_model_path"],
		Aggressiveness: atoiOr(params["ns_aggressiveness"], 0),
	}
	suppressor, err := model.NewSuppressor(suppCfg)
	if err != nil {
		return fmt.Errorf("build suppressor from custom_parameters: %w", err)
	}

	pipelineCfg := audio.PipelineConfig{
		Channels:        1,
		Suppressor:      suppressor,
		Logger:          s.logger,
		InputSampleRate: call.sampleRate,
	}
	switch params["ns_vad"] {
	case "adaptive":
		pipelineCfg.UseAdaptiveVAD = true
	case "static", "true":
		pipelineCfg.VADConfig = &audio.VADConfig{}
	}
	if params["ns_agc"] == "true" {
		agcCfg := audio.DefaultAGCConfig()
		if f, ok := parseFloatParam(params, "ns_agc_target_rms"); ok {
			agcCfg.TargetRMS = f
		}
		pipelineCfg.AGC = &agcCfg
		call.agcEnabled = true
	}
	if params["ns_mode"] == "adaptive" {
		tnrCfg := audio.DefaultTieredNRConfig()
		if f, ok := parseFloatParam(params, "ns_high_snr_db"); ok {
			tnrCfg.HighSNRThreshold = f
		}
		if f, ok := parseFloatParam(params, "ns_low_snr_db"); ok {
			tnrCfg.LowSNRThreshold = f
		}
		pipelineCfg.TieredNR = &tnrCfg
		call.tieredEnabled = true
	}

	call.pipeline = audio.NewPipeline(pipelineCfg)
	call.state = StateStreaming

	s.logger.Info("agentstream: call started",
		zap.String("stream_sid", call.streamSID),
		zap.String("backend", suppCfg.Backend),
		zap.Any("custom_parameters", params),
	)
	return nil
}

// handleMedia decodes one base64 PCM frame, runs it through this call's
// Pipeline, and emits the enhanced frame back as a clean_media event.
func (s *AgentStreamServer) handleMedia(conn *websocket.Conn, call *callState, ev *MediaEvent) error {
	call.mu.Lock()
	pipeline := call.pipeline
	streamSID := call.streamSID
	agcEnabled := call.agcEnabled
	tieredEnabled := call.tieredEnabled
	call.mu.Unlock()

	if pipeline == nil {
		return fmt.Errorf("media event received before start")
	}

	raw, err := base64.StdEncoding.DecodeString(ev.Payload)
	if err != nil {
		return fmt.Errorf("decode media payload: %w", err)
	}

	var outBuf bytes.Buffer
	if err := pipeline.ProcessFrames(raw, &outBuf); err != nil {
		return fmt.Errorf("pipeline process: %w", err)
	}
	if outBuf.Len() == 0 {
		// Buffered as a partial frame internally; nothing to emit yet.
		return nil
	}

	call.mu.Lock()
	call.seq++
	seq := call.seq
	call.mu.Unlock()

	out := CleanMediaEvent{
		Event:          EventCleanMedia,
		StreamSID:      streamSID,
		SequenceNumber: seq,
		Track:          ev.Track,
		Codec:          ev.Codec,
		SampleRate:     ev.SampleRate,
		TimestampMs:    ev.TimestampMs,
		Enhancement: EnhancementInfo{
			NoiseSuppression:  !pipeline.Bypassed(),
			GainNormalization: agcEnabled && !pipeline.Bypassed(),
			// TieredNR is reported under NoiseSuppression's umbrella; there is
			// no dedicated EnhancementInfo field for it today. VoiceIsolation,
			// BackgroundVoiceCancellation, and EchoCancellation are not
			// implemented by this integration and are always reported false
			// rather than claiming a capability that does not exist.
		},
		Payload: base64.StdEncoding.EncodeToString(outBuf.Bytes()),
	}
	_ = tieredEnabled // reserved for a future dedicated EnhancementInfo field
	return s.send(conn, out)
}

// handleReconfigure applies a live, mid-call change requested by the bot
// backend. See ReconfigureEvent's doc comment for the supported modes.
func (s *AgentStreamServer) handleReconfigure(call *callState, ev *ReconfigureEvent) error {
	call.mu.Lock()
	pipeline := call.pipeline
	streamSID := call.streamSID
	call.mu.Unlock()

	if pipeline == nil {
		return fmt.Errorf("reconfigure event received before start")
	}

	switch ev.Mode {
	case "disabled":
		pipeline.SetBypass(true)
	case "enabled":
		pipeline.SetBypass(false)
	case "adaptive", "aggressive", "mild":
		high, low := aggressivenessToThresholds(ev.Mode)
		pipeline.Reconfigure(audio.PipelineConfig{
			TieredNR: &audio.TieredNRConfig{HighSNRThreshold: high, LowSNRThreshold: low},
		})
		if ev.Level > 0 {
			pipeline.SetAggressiveness(ev.Level)
		}
	default:
		return fmt.Errorf("unknown reconfigure mode %q", ev.Mode)
	}

	s.logger.Info("agentstream: reconfigured",
		zap.String("stream_sid", streamSID),
		zap.String("mode", ev.Mode),
		zap.Int("level", ev.Level),
	)
	return nil
}

// aggressivenessToThresholds maps a reconfigure mode to TieredNR SNR
// boundaries. NOTE: this only adjusts thresholds on an ALREADY-configured
// TieredNR (Pipeline.Reconfigure is a no-op if TieredNR was never set at
// start via ns_mode=adaptive) -- enabling tiered reduction from nothing
// mid-call is not supported in this pass. Widening the band (mild) biases
// more frames toward the cheap gate-only tier; narrowing it (aggressive)
// biases more frames toward RNNoise/DeepFilter. A caller wanting exact
// values should set ns_high_snr_db/ns_low_snr_db at start instead.
func aggressivenessToThresholds(mode string) (high, low float64) {
	switch mode {
	case "mild":
		return 30, 5
	case "aggressive":
		return 20, 15
	default: // "adaptive"
		return 25, 10
	}
}

func (s *AgentStreamServer) handleStop(call *callState, ev *StopEvent) error {
	call.mu.Lock()
	defer call.mu.Unlock()
	if call.pipeline != nil {
		call.pipeline.Close()
	}
	call.state = StateCompleted
	return nil
}

func (s *AgentStreamServer) send(conn *websocket.Conn, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseFloatParam(params CustomParameters, key string) (float64, bool) {
	v, ok := params[key]
	if !ok || v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
