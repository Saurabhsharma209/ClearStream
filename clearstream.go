// Package clearstream provides the ClearStream audio enhancement SDK.
// It supports noise suppression for audio files, live RTP streams, and raw PCM pipelines.
package clearstream

import (
	"fmt"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/rtp"
	"go.uber.org/zap"
)

// Version is the current ClearStream SDK version.
const Version = "0.1.0"

// ClearStream is the main SDK entry point. Create one via New().
type ClearStream struct {
	cfg    Config
	model  model.Suppressor
	logger *zap.Logger
}

// Config holds the configuration for a ClearStream instance.
type Config struct {
	// Model selects the noise-suppression backend: "rnnoise", "deepfilter", or "passthrough".
	Model string
	// ModelPath is the path to the ONNX model file (required for deepfilter).
	ModelPath string
	// SampleRate is the audio sample rate in Hz. Must be one of 8000, 16000, 44100, or 48000.
	SampleRate int
	// Channels is the number of audio channels. Must be 1 (mono) or 2 (stereo).
	Channels int
	// FFmpegPath is the path or name of the ffmpeg binary. Defaults to "ffmpeg".
	FFmpegPath string
	// Logger is an optional zap logger. If nil, a production logger is created.
	Logger *zap.Logger
}

// Validate checks that the Config fields are within acceptable ranges.
// It returns an error describing the first invalid field found.
func (c Config) Validate() error {
	validRates := map[int]bool{8000: true, 16000: true, 44100: true, 48000: true}
	if !validRates[c.SampleRate] {
		return fmt.Errorf("clearstream: SampleRate %d is not supported; use 8000, 16000, 44100, or 48000", c.SampleRate)
	}
	if c.Channels != 1 && c.Channels != 2 {
		return fmt.Errorf("clearstream: Channels must be 1 or 2, got %d", c.Channels)
	}
	if c.FFmpegPath == "" {
		return fmt.Errorf("clearstream: FFmpegPath must not be empty")
	}
	validModels := map[string]bool{"rnnoise": true, "deepfilter": true, "passthrough": true}
	if !validModels[c.Model] {
		return fmt.Errorf("clearstream: Model %q is not supported; use rnnoise, deepfilter, or passthrough", c.Model)
	}
	return nil
}

// DefaultConfig returns a Config pre-populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Model:      "rnnoise",
		SampleRate: 16000,
		Channels:   1,
		FFmpegPath: "ffmpeg",
	}
}

// New creates and initialises a new ClearStream instance from the provided Config.
// It validates the config, sets up logging, and loads the noise-suppression model.
func New(cfg Config) (*ClearStream, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Channels == 0 {
		cfg.Channels = 1
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.Model == "" {
		cfg.Model = "rnnoise"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
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
	return &ClearStream{cfg: cfg, model: sup, logger: logger}, nil
}

// ProcessFile suppresses noise in the audio track of src and writes the result to dst.
// Both src and dst may be any format supported by ffmpeg.
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

// ProcessFileWithOptions is like ProcessFile but accepts additional file.Options,
// such as stripping the video track from the output.
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

// NewRTPSession creates a live RTP interception session that applies noise suppression
// to each incoming packet and forwards the cleaned audio to a downstream address.
func (cs *ClearStream) NewRTPSession(cfg rtp.Config) (*rtp.Session, error) {
	cfg.SampleRate = cs.cfg.SampleRate
	cfg.Suppressor = cs.model
	cfg.Logger = cs.logger
	return rtp.NewSession(cfg)
}

// Pipeline returns a raw audio.Pipeline for low-level frame-by-frame processing.
// Use this when you need direct control over PCM data ingestion and retrieval.
func (cs *ClearStream) Pipeline() *audio.Pipeline {
	return audio.NewPipeline(audio.PipelineConfig{
		SampleRate: cs.cfg.SampleRate,
		Channels:   cs.cfg.Channels,
		Suppressor: cs.model,
		Logger:     cs.logger,
	})
}

// Close releases all resources held by the ClearStream instance, including the
// loaded noise-suppression model. Always call Close when done.
func (cs *ClearStream) Close() error { return cs.model.Close() }
