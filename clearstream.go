// Package clearstream provides a codec-agnostic audio enhancement SDK.
// It supports real-time RTP stream processing and post-processing of
// audio/video files using AI-based noise suppression.
//
// Quick start — file cleanup:
//
//	cs, _ := clearstream.New(clearstream.DefaultConfig())
//	err := cs.ProcessFile("noisy.mp4", "clean.mp4")
//
// Quick start — live RTP:
//
//	cs, _ := clearstream.New(clearstream.DefaultConfig())
//	session, _ := cs.NewRTPSession(clearstream.RTPConfig{
//	    ListenAddr: ":5004",
//	    ForwardAddr: "10.0.0.2:5004",
//	})
//	session.Start()
package clearstream

import (
	"fmt"

	"net/http"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/rtp"
	"go.uber.org/zap"
)

// Version is the ClearStream SDK version.
const Version = "0.1.0"

// ClearStream is the main SDK entry point.
type ClearStream struct {
	cfg    Config
	model  model.Suppressor
	logger *zap.Logger
}

// Config holds top-level SDK configuration.
type Config struct {
	// Model selects the noise suppression backend.
	// Options: "rnnoise" (default, CPU, no deps), "deepfilter" (ONNX, higher quality)
	Model string

	// ModelPath is the path to the ONNX model file (required for "deepfilter").
	ModelPath string

	// SampleRate is the internal processing sample rate. Default: 16000.
	SampleRate int

	// Channels for processing. Default: 1 (mono).
	Channels int

	// FFmpegPath overrides the ffmpeg binary location. Default: "ffmpeg" (PATH).
	FFmpegPath string

	// Logger is an optional zap logger. If nil, production logger is used.
	Logger *zap.Logger

	// EnableVAD enables Voice Activity Detection to skip suppression on silence
	// frames, saving ~30% CPU on typical telephony calls. Default: false.
	EnableVAD bool

	// AdaptiveVAD enables adaptive noise floor calibration instead of the
	// static energy threshold VAD. Requires EnableVAD=true. Default: false.
	AdaptiveVAD bool

	// VADThreshold is the RMS energy threshold for static VAD (EnableVAD=true,
	// AdaptiveVAD=false). Default: 300 (good for 16-bit telephony PCM).
	VADThreshold float64
}

// DefaultConfig returns a sensible out-of-the-box configuration.
func DefaultConfig() Config {
	return Config{
		Model:      "rnnoise",
		SampleRate: 16000,
		Channels:   1,
		FFmpegPath: "ffmpeg",
	}
}

// Validate checks Config fields and returns an error describing the first
// invalid value found. Call before New() to get clear error messages.
func (c *Config) Validate() error {
	if c.SampleRate != 0 && (c.SampleRate < 8000 || c.SampleRate > 48000) {
		return fmt.Errorf("clearstream: SampleRate %d out of range [8000, 48000]", c.SampleRate)
	}
	if c.Channels != 0 && (c.Channels < 1 || c.Channels > 2) {
		return fmt.Errorf("clearstream: Channels %d out of range [1, 2]", c.Channels)
	}
	validModels := map[string]bool{"": true, "rnnoise": true, "deepfilter": true, "passthrough": true}
	if !validModels[c.Model] {
		return fmt.Errorf("clearstream: unknown Model %q (valid: rnnoise, deepfilter, passthrough)", c.Model)
	}
	if c.Model == "deepfilter" && c.ModelPath == "" {
		return fmt.Errorf("clearstream: Model \"deepfilter\" requires ModelPath")
	}
	return nil
}

// New creates a ClearStream instance with the given config.
func New(cfg Config) (*ClearStream, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Channels == 0 {
		cfg.Channels = 1
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}

	logger := cfg.Logger
	if logger == nil {
		var err error
		logger, err = zap.NewProduction()
		if err != nil {
			return nil, fmt.Errorf("clearstream: init logger: %w", err)
		}
	}

	sup, err := model.NewSuppressor(model.SuppressorConfig{
		Backend:   cfg.Model,
		ModelPath: cfg.ModelPath,
	})
	if err != nil {
		return nil, fmt.Errorf("clearstream: init model: %w", err)
	}

	return &ClearStream{
		cfg:    cfg,
		model:  sup,
		logger: logger,
	}, nil
}

// ProcessFile enhances audio in src and writes the result to dst.
// Both audio files (mp3, wav, flac, ogg, aac) and video files
// (mp4, mkv, mov, avi, webm) are supported. The audio track is
// cleaned and muxed back into the container transparently.
func (cs *ClearStream) ProcessFile(src, dst string) error {
	fp := file.NewProcessor(file.ProcessorConfig{
		FFmpegPath: cs.cfg.FFmpegPath,
		SampleRate: cs.cfg.SampleRate,
		Channels:   cs.cfg.Channels,
		Suppressor: cs.model,
		Logger:     cs.logger,
	})
	return fp.Process(src, dst)
}

// ProcessFileWithOptions is like ProcessFile but accepts per-call options.
func (cs *ClearStream) ProcessFileWithOptions(src, dst string, opts file.Options) error {
	fp := file.NewProcessor(file.ProcessorConfig{
		FFmpegPath: cs.cfg.FFmpegPath,
		SampleRate: cs.cfg.SampleRate,
		Channels:   cs.cfg.Channels,
		Suppressor: cs.model,
		Logger:     cs.logger,
	})
	return fp.ProcessWithOptions(src, dst, opts)
}

// NewRTPSession creates a live RTP interception session.
// The session reads RTP packets from ListenAddr, suppresses noise,
// and forwards clean packets to ForwardAddr.
func (cs *ClearStream) NewRTPSession(cfg rtp.Config) (*rtp.Session, error) {
	cfg.SampleRate = cs.cfg.SampleRate
	cfg.Suppressor = cs.model
	cfg.Logger = cs.logger
	return rtp.NewSession(cfg)
}

// Pipeline returns a low-level audio processing pipeline for advanced use.
// Use this when you want to feed raw PCM frames directly.
func (cs *ClearStream) Pipeline() *audio.Pipeline {
	var vad audio.VADer
	if cs.cfg.EnableVAD {
		if cs.cfg.AdaptiveVAD {
			vad = audio.DefaultAdaptiveVAD()
		} else {
			threshold := cs.cfg.VADThreshold
			if threshold == 0 {
				threshold = 300
			}
			vad = &audio.VAD{ThresholdRMS: threshold, HangoverFrames: 8}
		}
	}
	return audio.NewPipeline(audio.PipelineConfig{
		SampleRate: cs.cfg.SampleRate,
		Channels:   cs.cfg.Channels,
		Suppressor: cs.model,
		Logger:     cs.logger,
		VAD:        vad,
	})
}

// PipelineStats returns a snapshot of the current pipeline metrics.
// Useful for monitoring frames processed, suppression ratio, and latency.
func (cs *ClearStream) PipelineStats() audio.PipelineStats {
	return cs.Pipeline().Stats()
}

// Close releases resources held by the SDK (model handles, etc.).
func (cs *ClearStream) Close() error {
	return cs.model.Close()
}

// NewHTTPHandler returns an http.Handler exposing the ClearStream API.
// AgentStream integrates via POST /enhance, GET /health, GET /metrics.
// Mount it: http.Handle("/", cs.NewHTTPHandler())
func (cs *ClearStream) NewHTTPHandler() http.Handler {
	return cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: cs.model,
		FFmpegPath: cs.cfg.FFmpegPath,
		SampleRate: cs.cfg.SampleRate,
		Logger:     cs.logger,
	})
}
