// Package model defines the Suppressor interface and provides
// concrete implementations (RNNoise, DeepFilterNet, passthrough).
package model

import (
	"fmt"

	"go.uber.org/zap"
)

// Suppressor is the core AI interface. All backends implement this.
// It receives raw 16kHz mono int16 PCM frames and returns cleaned frames.
// Implementations must be goroutine-safe if used across concurrent streams.
type Suppressor interface {
	// Process takes a frame of 16kHz mono PCM (typically 160 samples = 10ms)
	// and returns a clean frame of the same length.
	Process(frame []int16) ([]int16, error)

	// Reset clears any internal state (useful when switching audio streams).
	Reset()

	// Close releases underlying resources (CGo handles, ONNX sessions, etc.).
	Close() error

	// Name returns a human-readable identifier for the backend.
	Name() string
}

// SuppressorConfig configures which backend to load.
// Backend selects the suppression engine: "rnnoise" for the lightweight RNNoise
// CGo backend (default when empty), "deepfilter" for the ONNX-based DeepFilterNet
// model, or "passthrough" for a no-op implementation suited to tests and pipelines
// that do not require noise suppression.
// ModelPath is required only when Backend is "deepfilter"; it must point to a
// valid ONNX model file on disk.
type SuppressorConfig struct {
	// Backend selects the noise-suppression engine.
	// Valid values: "rnnoise" (default), "deepfilter", "passthrough".
	Backend string

	// ModelPath is the path to the ONNX model file.
	// Required when Backend is "deepfilter"; ignored otherwise.
	ModelPath string

	// Aggressiveness controls suppression strength: 0=backend default, 1=mild,
	// 2=medium, 3=aggressive. The passthrough backend ignores this field.
	// RNNoise and future backends use it to tune their internal parameters.
	Aggressiveness int

	// ServerURL is the base URL of the DeepFilterNet Python inference server.
	// Used when Backend is "deepfilter-server". Default: "http://127.0.0.1:7878".
	// Start the server with: python3 scripts/df_server.py
	ServerURL string

	// AutoStartPath is the path to df_server.py.
	// When set and Backend is "deepfilter-server", ClearStream will auto-start
	// the Python server if it is not already running.
	// Example: "scripts/df_server.py"
	AutoStartPath string
}

// DefaultSuppressorConfig returns a SuppressorConfig using the passthrough
// backend, suitable for testing without any external dependencies.
func DefaultSuppressorConfig() SuppressorConfig {
	return SuppressorConfig{Backend: "passthrough"}
}

// NewSuppressor constructs the appropriate Suppressor based on config.
func NewSuppressor(cfg SuppressorConfig) (Suppressor, error) {
	switch cfg.Backend {
	case "rnnoise", "":
		return NewRNNoise()
	case "rnnoise-onnx":
		if cfg.ModelPath == "" {
			return nil, fmt.Errorf("model: rnnoise-onnx requires ModelPath")
		}
		logger, _ := zap.NewProduction()
		return NewRNNoiseONNX(cfg.ModelPath, logger)
	case "deepfilter":
		if cfg.ModelPath == "" {
			return nil, fmt.Errorf("model: deepfilter requires ModelPath")
		}
		logger, _ := zap.NewProduction()
		return newDeepFilterSuppressor(cfg.ModelPath, logger)
	case "deepfilter-server":
		logger, _ := zap.NewProduction()
		return newDeepFilterServerSuppressor(cfg.ServerURL, cfg.AutoStartPath, logger)
	case "passthrough":
		return NewPassthrough(), nil
	default:
		return nil, fmt.Errorf("model: unknown backend %q (valid: rnnoise, rnnoise-onnx, deepfilter, deepfilter-server, passthrough)", cfg.Backend)
	}
}

// BatchSuppressor extends Suppressor with a batch-processing method.
// Implementations may process frames more efficiently in bulk (e.g. SIMD).
// The default BatchWrapper provides a sequential fallback for any Suppressor.
type BatchSuppressor interface {
	Suppressor
	// ProcessBatch processes multiple frames in one call. Each frame must be
	// the same length. Returns processed frames in the same order, or an error
	// if any frame fails (remaining frames are returned as-is on error).
	ProcessBatch(frames [][]int16) ([][]int16, error)
}

// AsBatch wraps any Suppressor in a BatchWrapper that implements BatchSuppressor.
// If s already implements BatchSuppressor, it is returned unwrapped.
func AsBatch(s Suppressor) BatchSuppressor {
	if bs, ok := s.(BatchSuppressor); ok {
		return bs
	}
	return &BatchWrapper{s: s}
}
