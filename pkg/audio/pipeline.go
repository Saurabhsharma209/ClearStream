package audio

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

const (
	// FrameSizeSamples is the number of PCM samples per processing frame (10ms @ 16kHz).
	FrameSizeSamples = 160
	// FrameSizeBytes is FrameSizeSamples * 2 (int16 = 2 bytes).
	FrameSizeBytes = FrameSizeSamples * 2
)

// framePool reduces allocations in the ProcessFrames hot path.
// Each pooled buffer is large enough for a 16kHz frame after resampling.
var framePool = sync.Pool{New: func() interface{} { b := make([]byte, FrameSizeBytes*4); return &b }}

// VADer is the interface satisfied by both VAD and AdaptiveVAD.
// Any type that can classify a PCM frame as speech and reset its state
// can be used as a voice activity detector in the pipeline.
type VADer interface {
	IsSpeech([]int16) bool
	Reset()
}

// PipelineConfig configures a Pipeline.
type PipelineConfig struct {
	SampleRate int
	Channels   int
	Suppressor model.Suppressor
	Logger     *zap.Logger
	// VAD is an optional Voice Activity Detector. When set, silence frames
	// bypass the suppressor entirely, saving ~30% CPU on typical calls.
	// Accepts *VAD (static threshold) or *AdaptiveVAD (auto-calibrating).
	VAD VADer
	// UseAdaptiveVAD, when true and VAD is nil, causes NewPipeline to
	// automatically create a DefaultAdaptiveVAD() that calibrates the noise
	// floor over the first 500ms of audio. Set VAD explicitly to override.
	UseAdaptiveVAD bool

	// Diarizer is an optional speaker diarization engine.
	// When set, each frame's speaker label is tracked alongside suppression.
	// Use NewEnergyDiarizer(DefaultEnergyDiarizerConfig()) for energy-based diarization.
	Diarizer Diarizer

	// AGC is optional Automatic Gain Control applied after noise suppression.
	// When set, the pipeline adaptively adjusts output level toward AGC.TargetRMS.
	// Use DefaultAGCConfig() as a starting point for telephony calls.
	// Set to nil to disable (default).
	AGC *AGCConfig

	// AEC is optional Acoustic Echo Cancellation applied before VAD and suppression.
	// Feed the far-end reference signal via Pipeline.SetFarEnd() before each ProcessFrames call.
	// Set to nil to disable (default).
	AEC *AECConfig

	// InputSampleRate is the sample rate of incoming PCM audio in Hz.
	// When 0, defaults to 8000 (narrowband, backward-compatible with Indian PSTN).
	// The suppressor always operates at ProcessorSampleRate (16kHz); the pipeline
	// resamples the input before suppression and back to InputSampleRate afterward.
	// If InputSampleRate == ProcessorSampleRate (16000), no resampling is done.
	InputSampleRate int

	// UseNoiseReducer enables the built-in AdaptiveNoiseReducer which runs
	// BEFORE the Suppressor. Recommended for telephony environments with
	// sustained background noise (HVAC, line hum, open-office). Replaces the
	// previous passthrough/spectral-gate approach with genuine multi-band
	// Wiener gain reduction. Set true to enable.
	UseNoiseReducer bool

	// UseLimiter enables the PeakLimiter stage applied AFTER AGC and BEFORE
	// the final output write. Prevents clipping caused by burst events, DTMF
	// tones, or AGC overshoot on sudden loud frames. Set true to enable.
	UseLimiter bool
}

// PipelineStats holds real-time pipeline quality metrics.
type PipelineStats struct {
	FramesProcessed  uint64  // total frames through pipeline
	FramesSuppressed uint64  // frames sent through AI suppressor (non-silent)
	FramesSilent     uint64  // frames bypassed via VAD
	AvgLatencyMs     float64 // exponential moving average of per-frame latency
	SuppressRatio    float64 // FramesSuppressed / FramesProcessed (0-1)
}

// Pipeline processes raw 16kHz mono PCM frames through the AI suppressor.
// Feed frames via Write; read clean frames via Read.
// This is the inner loop used by both the file processor and RTP session.
type Pipeline struct {
	cfg    PipelineConfig
	buf    []byte // partial frame accumulator
	vad    VADer
	agc    *AGC
	logger *zap.Logger

	aec      *AEC
	farEnd   []int16 // far-end reference for AEC (set by SetFarEnd)
	farEndMu sync.Mutex

	diarizer Diarizer

	// Optional noise reducer (runs before suppressor).
	noiseReducer *AdaptiveNoiseReducer

	// Optional peak limiter (runs after AGC, before output write).
	limiter *PeakLimiter

	statsMu          sync.Mutex
	framesProcessed  uint64
	framesSuppressed uint64
	framesSilent     uint64
	latencyEMA       float64
}

// NewPipeline creates a new Pipeline.
// If cfg.UseAdaptiveVAD is true and cfg.VAD is nil, a DefaultAdaptiveVAD()
// is created automatically to calibrate the noise floor over the first 500ms.
// If cfg.UseNoiseReducer is true, an AdaptiveNoiseReducer is created and
// applied before the configured Suppressor.
// If cfg.UseLimiter is true, a PeakLimiter is applied after AGC.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	vad := cfg.VAD
	if vad == nil && cfg.UseAdaptiveVAD {
		vad = DefaultAdaptiveVAD()
	}
	var agc *AGC
	if cfg.AGC != nil {
		agcCfg := *cfg.AGC
		agcCfg.SampleRate = cfg.SampleRate
		if agcCfg.SampleRate == 0 {
			agcCfg.SampleRate = 16000
		}
		agc = NewAGC(agcCfg)
	}
	var aec *AEC
	if cfg.AEC != nil {
		aecCfg := *cfg.AEC
		if aecCfg.SampleRate == 0 {
			aecCfg.SampleRate = cfg.SampleRate
		}
		aec = NewAEC(aecCfg)
	}

	var nr *AdaptiveNoiseReducer
	if cfg.UseNoiseReducer {
		nr = NewAdaptiveNoiseReducer()
	}

	var lim *PeakLimiter
	if cfg.UseLimiter {
		lim = NewPeakLimiter()
	}

	return &Pipeline{
		cfg:          cfg,
		buf:          make([]byte, 0, FrameSizeBytes*4),
		vad:          vad,
		agc:          agc,
		aec:          aec,
		noiseReducer: nr,
		limiter:      lim,
		logger:       cfg.Logger,
	}
}

// inputRate returns the effective input sample rate.
// Priority: InputSampleRate > SampleRate > 8000 (narrowband fallback for Indian PSTN).
func (p *Pipeline) inputRate() int {
	if p.cfg.InputSampleRate > 0 {
		return p.cfg.InputSampleRate
	}
	if p.cfg.SampleRate > 0 {
		return p.cfg.SampleRate
	}
	return 8000 // safe narrowband fallback (Indian PSTN: G.711 µ-law/A-law)
}

// ProcessFrames reads all available complete frames from in, runs suppression,
// and writes clean PCM to out. Partial trailing data is buffered for the next call.
// If VAD is configured, silence frames bypass suppression to save CPU.
//
// Processing order per frame:
//  1. Resample to 16kHz (if needed)
//  2. AEC (if configured)
//  3. AdaptiveNoiseReducer (if UseNoiseReducer)
//  4. VAD gate → Suppressor (if speech) or passthrough (if silence)
//  5. AGC (if configured)
//  6. PeakLimiter (if UseLimiter)
//  7. Resample back to input rate (if needed)
//  8. Diarizer (if configured)
//
// Resampling behaviour (governed by InputSampleRate):
//   - 0 or 8000  → upsample 8kHz→16kHz before suppression, downsample back after
//   - 16000      → no resampling (suppressor native rate)
//   - >16000     → downsample to 16kHz before suppression, upsample back after
func (p *Pipeline) ProcessFrames(in []byte, out io.Writer) error {
	inRate := p.inputRate()

	// Prepend any leftover bytes from last call
	data := append(p.buf, in...)
	p.buf = p.buf[:0]

	// Frame size in bytes for the input rate. At 16kHz, 10ms = 160 samples = 320 bytes.
	// At other rates, scale proportionally.
	inputFrameBytes := FrameSizeBytes
	if inRate != ProcessorSampleRate {
		inputFrameBytes = FrameSizeSamples * inRate / ProcessorSampleRate * 2
		if inputFrameBytes <= 0 {
			inputFrameBytes = FrameSizeBytes
		}
	}

	offset := 0
	for offset+inputFrameBytes <= len(data) {
		frame := data[offset : offset+inputFrameBytes]
		offset += inputFrameBytes

		start := time.Now()

		// Convert bytes -> int16 samples
		samples := bytesToInt16(frame)

		// Resample to ProcessorSampleRate (16kHz) if needed.
		processSamples := samples
		if inRate != ProcessorSampleRate {
			var err error
			processSamples, err = Resample(samples, inRate, ProcessorSampleRate)
			if err != nil {
				return fmt.Errorf("pipeline: resample input %d→%d: %w", inRate, ProcessorSampleRate, err)
			}
		}

		// AEC: cancel echo from near-end using far-end reference
		if p.aec != nil {
			p.farEndMu.Lock()
			fe := p.farEnd
			p.farEndMu.Unlock()
			processSamples = p.aec.Process(fe, processSamples)
		}

		// Adaptive noise reduction — runs before suppressor on every frame.
		if p.noiseReducer != nil {
			var err error
			processSamples, err = p.noiseReducer.Process(processSamples)
			if err != nil {
				return fmt.Errorf("pipeline: noise reducer: %w", err)
			}
		}

		var cleaned []int16
		usedSuppressor := false
		if p.vad != nil && !p.vad.IsSpeech(processSamples) {
			// silence -- pass through without suppression (saves CPU)
			cleaned = processSamples
		} else {
			var err error
			cleaned, err = p.cfg.Suppressor.Process(processSamples)
			if err != nil {
				return fmt.Errorf("pipeline: suppress frame: %w", err)
			}
			usedSuppressor = true
		}

		// AGC: adaptive gain applied after suppression (speech frames only)
		if p.agc != nil {
			cleaned = p.agc.Process(cleaned)
		}

		// Peak limiter: guards against clipping after AGC or burst events.
		if p.limiter != nil {
			cleaned = p.limiter.Process(cleaned)
		}

		// Resample back to the original input rate if needed.
		outSamples := cleaned
		if inRate != ProcessorSampleRate {
			var err error
			outSamples, err = Resample(cleaned, ProcessorSampleRate, inRate)
			if err != nil {
				return fmt.Errorf("pipeline: resample output %d→%d: %w", ProcessorSampleRate, inRate, err)
			}
		}

		if p.diarizer != nil {
			p.diarizer.ProcessFrame(outSamples, time.Now().UnixMilli())
		}

		elapsed := time.Since(start).Seconds() * 1000

		p.statsMu.Lock()
		p.framesProcessed++
		if usedSuppressor {
			p.framesSuppressed++
		} else {
			p.framesSilent++
		}
		p.latencyEMA = p.latencyEMA*0.95 + elapsed*0.05
		p.statsMu.Unlock()

		// Write cleaned frame (uses pooled scratch buffer to reduce GC pressure).
		outBytes := int16ToBytes(outSamples)
		if _, err := out.Write(outBytes); err != nil {
			return fmt.Errorf("pipeline: write frame: %w", err)
		}
	}

	// Buffer leftover bytes
	if offset < len(data) {
		p.buf = append(p.buf, data[offset:]...)
	}
	return nil
}

// Flush processes any buffered partial frame (zero-padded to FrameSizeBytes).
// Call after the last ProcessFrames to drain the buffer.
func (p *Pipeline) Flush(out io.Writer) error {
	if len(p.buf) == 0 {
		return nil
	}
	// Zero-pad to full frame
	frame := make([]byte, FrameSizeBytes)
	copy(frame, p.buf)
	p.buf = p.buf[:0]
	samples := bytesToInt16(frame)
	cleaned, err := p.cfg.Suppressor.Process(samples)
	if err != nil {
		return fmt.Errorf("pipeline: flush suppress: %w", err)
	}
	_, err = out.Write(int16ToBytes(cleaned))
	return err
}

// String returns a human-readable summary of pipeline stats.
func (s PipelineStats) String() string {
	return fmt.Sprintf("frames=%d suppressed=%d silent=%d ratio=%.1f%% latency=%.2fms",
		s.FramesProcessed, s.FramesSuppressed, s.FramesSilent,
		s.SuppressRatio*100, s.AvgLatencyMs)
}

// Stats returns a snapshot of pipeline metrics.
func (p *Pipeline) Stats() PipelineStats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	ratio := float64(0)
	if p.framesProcessed > 0 {
		ratio = float64(p.framesSuppressed) / float64(p.framesProcessed)
	}
	return PipelineStats{
		FramesProcessed:  p.framesProcessed,
		FramesSuppressed: p.framesSuppressed,
		FramesSilent:     p.framesSilent,
		AvgLatencyMs:     p.latencyEMA,
		SuppressRatio:    ratio,
	}
}

// ResetStats clears only the pipeline counters and latency EMA, leaving the
// audio processing state (VAD, AGC, AEC, suppressor) untouched.
// Use this for periodic per-interval reporting (e.g. emit metrics every 60s
// then reset so the next interval starts fresh) without disrupting the call.
// Thread-safe.
func (p *Pipeline) ResetStats() {
	p.statsMu.Lock()
	p.framesProcessed = 0
	p.framesSuppressed = 0
	p.framesSilent = 0
	p.latencyEMA = 0
	p.statsMu.Unlock()
}

// Reset clears internal state (call when starting a new stream/file).
func (p *Pipeline) Reset() {
	p.buf = p.buf[:0]
	p.cfg.Suppressor.Reset()
	if p.vad != nil {
		p.vad.Reset()
	}
	if p.agc != nil {
		p.agc.Reset()
	}
	if p.aec != nil {
		p.aec.Reset()
	}
	if p.noiseReducer != nil {
		p.noiseReducer.Reset()
	}
	if p.limiter != nil {
		p.limiter.Reset()
	}
	p.statsMu.Lock()
	p.framesProcessed = 0
	p.framesSuppressed = 0
	p.framesSilent = 0
	p.latencyEMA = 0
	p.statsMu.Unlock()
}

// SetFarEnd provides the far-end reference signal for AEC.
// Call this with the decoded far-end PCM before each ProcessFrames call.
// Thread-safe.
func (p *Pipeline) SetFarEnd(samples []int16) {
	p.farEndMu.Lock()
	p.farEnd = samples
	p.farEndMu.Unlock()
}

// ---- helpers ----------------------------------------------------------------

func bytesToInt16(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(b[2*i]) | int16(b[2*i+1])<<8
	}
	return out
}

func int16ToBytes(s []int16) []byte {
	out := make([]byte, len(s)*2)
	for i, v := range s {
		out[2*i] = byte(v)
		out[2*i+1] = byte(v >> 8)
	}
	return out
}

// DiarizationSegments returns all completed speaker segments if a Diarizer is configured.
func (p *Pipeline) DiarizationSegments() []DiarizedSegment {
	if p.diarizer == nil {
		return nil
	}
	return p.diarizer.Segments()
}
