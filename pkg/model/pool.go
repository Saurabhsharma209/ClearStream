package model

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// SuppressorPool manages a fixed pool of Suppressors for concurrent use.
// Each caller acquires one Suppressor for a call leg, then releases it back.
// This avoids per-session allocation for stateful backends like RNNoise.
type SuppressorPool struct {
	pool      chan Suppressor
	cfg       SuppressorConfig
	mu        sync.Mutex
	size      int
	closeOnce sync.Once
	closed    int32 // 0 = open, 1 = closed; accessed via sync/atomic (Go 1.17 has no atomic.Bool)
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
//
// If the pool has already been closed (via Close), Acquire returns nil rather
// than blocking forever or panicking: receiving from a closed, drained channel
// yields a nil Suppressor immediately, and calling Reset on that nil value
// would otherwise panic with a nil-interface dereference. Callers should treat
// a nil return as "pool unavailable" and must not call methods on it.
func (p *SuppressorPool) Acquire() Suppressor {
	s, ok := <-p.pool
	if !ok || s == nil {
		return nil
	}
	s.Reset()
	return s
}

// Release returns a Suppressor to the pool.
//
// If the pool has already been closed, Release closes s directly instead of
// sending it back on the (now closed) channel, which would otherwise panic
// with "send on closed channel". This makes Release safe to call from
// in-flight sessions that are winding down concurrently with pool shutdown.
func (p *SuppressorPool) Release(s Suppressor) {
	if atomic.LoadInt32(&p.closed) == 1 {
		if s != nil {
			_ = s.Close()
		}
		return
	}
	p.pool <- s
}

// Size returns the pool capacity.
func (p *SuppressorPool) Size() int { return p.size }

// Close shuts down all pooled Suppressors. Safe to call more than once.
func (p *SuppressorPool) Close() error {
	var firstErr error
	p.closeOnce.Do(func() {
		atomic.StoreInt32(&p.closed, 1)
		close(p.pool)
		for s := range p.pool {
			if err := s.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// WarmPool ensures the pool contains exactly n ready Suppressors, blocking
// until all are initialised. It is safe to call at startup before any
// sessions begin. Returns an error if n exceeds pool capacity, if the pool
// has already been closed, or any Suppressor fails to initialise.
func (p *SuppressorPool) WarmPool(n int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if atomic.LoadInt32(&p.closed) == 1 {
		return fmt.Errorf("model: WarmPool called on a closed pool")
	}
	if n > p.size {
		return fmt.Errorf("model: WarmPool(%d) exceeds pool capacity %d", n, p.size)
	}
	// If the pool already holds at least n items it's a no-op.
	if len(p.pool) >= n {
		return nil
	}
	// Drain whatever is currently in the pool, closing each suppressor.
	draining := true
	for draining {
		select {
		case s := <-p.pool:
			_ = s.Close()
		default:
			draining = false
		}
	}
	// Refill with n fresh suppressors.
	for i := 0; i < n; i++ {
		s, err := NewSuppressor(p.cfg)
		if err != nil {
			return fmt.Errorf("model: WarmPool init [%d/%d]: %w", i+1, n, err)
		}
		p.pool <- s
	}
	return nil
}
