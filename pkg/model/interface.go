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
	case "deepfilter":
		if cfg.ModelPath == "" {
			return nil, fmt.Errorf("model: deepfilter requires ModelPath")
		}
		logger, _ := zap.NewProduction()
		return newDeepFilterSuppressor(cfg.ModelPath, logger)
	case "passthrough":
		return NewPassthrough(), nil
	default:
		return nil, fmt.Errorf("model: unknown backend %q (valid: rnnoise, deepfilter, passthrough)", cfg.Backend)
	}
}
