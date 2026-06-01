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
type SuppressorConfig struct {
	// Backend: "rnnoise" | "deepfilter" | "passthrough"
	Backend string

	// ModelPath is the ONNX model file path (required for "deepfilter").
	ModelPath string
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
