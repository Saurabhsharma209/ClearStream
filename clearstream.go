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

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/rtp"
	"go.uber.org/zap"
)

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

// New creates a ClearStream instance with the given config.
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
	return audio.NewPipeline(audio.PipelineConfig{
		SampleRate: cs.cfg.SampleRate,
		Channels:   cs.cfg.Channels,
		Suppressor: cs.model,
		Logger:     cs.logger,
	})
}

// Close releases resources held by the SDK (model handles, etc.).
func (cs *ClearStream) Close() error {
	return cs.model.Close()
}
