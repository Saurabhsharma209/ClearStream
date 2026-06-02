package model

import (
	"fmt"
	"sync"
)

// SuppressorPool manages a fixed pool of Suppressors for concurrent use.
// Each caller acquires one Suppressor for a call leg, then releases it back.
// This avoids per-session allocation for stateful backends like RNNoise.
type SuppressorPool struct {
	pool chan Suppressor
	cfg  SuppressorConfig
	mu   sync.Mutex
	size int
}

// NewSuppressorPool creates a pool of n Suppressors using the given config.
func NewSuppressorPool(cfg SuppressorConfig, n int) (*SuppressorPool, error) {
	if n <= 0 {
		return nil, fmt.Errorf("model: pool size must be > 0, got %d", n)
	}
	p := &SuppressorPool{pool: make(chan Suppressor, n), cfg: cfg, size: n}
	for i := 0; i < n; i++ {
		s, err := NewSuppressor(cfg)
		if err != nil {
			return nil, fmt.Errorf("model: pool init [%d/%d]: %w", i+1, n, err)
		}
		p.pool <- s
	}
	return p, nil
}

// Acquire returns a Suppressor from the pool, blocking until one is available.
// Caller MUST call Release when the session ends.
func (p *SuppressorPool) Acquire() Suppressor {
	s := <-p.pool
	s.Reset()
	return s
}

// Release returns a Suppressor to the pool.
func (p *SuppressorPool) Release(s Suppressor) { p.pool <- s }

// Size returns the pool capacity.
func (p *SuppressorPool) Size() int { return p.size }

// Close shuts down all pooled Suppressors. Do not use after Close.
func (p *SuppressorPool) Close() error {
	close(p.pool)
	var firstErr error
	for s := range p.pool {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
