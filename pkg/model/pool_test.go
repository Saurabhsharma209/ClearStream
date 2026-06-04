package model

import (
	"context"
	"sync"
	"testing"
	"time"
)

func passthroughCfg() SuppressorConfig {
	return SuppressorConfig{Backend: "passthrough"}
}

// TestSuppressorPoolBasic verifies basic acquire/release semantics.
func TestSuppressorPoolBasic(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 3)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if pool.Size() != 3 {
		t.Errorf("Size() = %d, want 3", pool.Size())
	}

	s1 := pool.Acquire()
	s2 := pool.Acquire()
	s3 := pool.Acquire()

	// Pool is now empty; 4th Acquire should block — detect with select + default.
	blocked := make(chan struct{})
	go func() {
		select {
		case <-pool.pool:
			// should not happen immediately
		default:
			close(blocked)
		}
	}()

	select {
	case <-blocked:
		// good: pool was empty
	case <-time.After(100 * time.Millisecond):
		t.Error("expected pool to be empty after acquiring all 3 suppressors")
	}

	// Release one, then re-acquire.
	pool.Release(s3)
	s3again := pool.Acquire()
	if s3again == nil {
		t.Error("expected non-nil suppressor after release")
	}

	pool.Release(s1)
	pool.Release(s2)
	pool.Release(s3again)
}

// TestSuppressorPoolConcurrent runs 8 goroutines against a pool of 4.
func TestSuppressorPoolConcurrent(t *testing.T) {
	const poolSize = 4
	const goroutines = 8
	const framesPerGoroutine = 10

	pool, err := NewSuppressorPool(passthroughCfg(), poolSize)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(i)
	}

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			s := pool.Acquire()
			defer pool.Release(s)
			for i := 0; i < framesPerGoroutine; i++ {
				_, err := s.Process(frame)
				if err != nil {
					t.Errorf("Process: %v", err)
					return
				}
			}
		}()
	}

	select {
	case <-done:
		// all goroutines finished without deadlock
	case <-ctx.Done():
		t.Fatal("timeout: possible deadlock in concurrent pool usage")
	}
}

// TestSuppressorPoolInvalidSize ensures n=0 returns an error.
func TestSuppressorPoolInvalidSize(t *testing.T) {
	_, err := NewSuppressorPool(passthroughCfg(), 0)
	if err == nil {
		t.Error("expected error for pool size 0, got nil")
	}
}

// TestSuppressorPoolClose creates a pool of 2 and closes it without panic.
func TestSuppressorPoolClose(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Errorf("Close() returned unexpected error: %v", err)
	}
}

// TestSuppressorPoolReset verifies that Acquire calls Reset on the suppressor.
// It uses MockSuppressor injected directly into the pool channel so we can
// inspect ResetCalls after Acquire.
func TestSuppressorPoolReset(t *testing.T) {
	// Build a pool manually so we can inject a MockSuppressor.
	mock := NewMockSuppressor()
	p := &SuppressorPool{
		pool: make(chan Suppressor, 1),
		cfg:  passthroughCfg(),
		size: 1,
	}
	p.pool <- mock

	if mock.ResetCalls != 0 {
		t.Fatalf("expected 0 ResetCalls before Acquire, got %d", mock.ResetCalls)
	}

	s := p.Acquire()
	if mock.ResetCalls != 1 {
		t.Errorf("expected 1 ResetCalls after Acquire, got %d", mock.ResetCalls)
	}

	p.Release(s)

	// Acquire again — Reset should be called a second time.
	p.Acquire()
	if mock.ResetCalls != 2 {
		t.Errorf("expected 2 ResetCalls after second Acquire, got %d", mock.ResetCalls)
	}
}

// TestWarmPool verifies that WarmPool pre-fills the pool with n fresh
// Suppressors, that Size() still reports the original capacity, and that
// all n slots are immediately acquirable without blocking.
func TestWarmPool(t *testing.T) {
	const poolSize = 4

	pool, err := NewSuppressorPool(passthroughCfg(), poolSize)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if err := pool.WarmPool(poolSize); err != nil {
		t.Fatalf("WarmPool(%d): %v", poolSize, err)
	}

	// Capacity should still be the original size.
	if pool.Size() != poolSize {
		t.Errorf("Size() = %d, want %d", pool.Size(), poolSize)
	}

	// All 4 slots must be acquirable non-blocking (pool channel has them ready).
	acquired := make([]Suppressor, poolSize)
	for i := 0; i < poolSize; i++ {
		var s Suppressor
		select {
		case s = <-pool.pool:
			s.Reset()
		default:
			t.Fatalf("Acquire [%d]: pool was empty, WarmPool did not pre-fill", i)
		}
		if s == nil {
			t.Fatalf("Acquire [%d]: got nil Suppressor", i)
		}
		acquired[i] = s
	}

	// Release all back.
	for _, s := range acquired {
		pool.Release(s)
	}

	// WarmPool on a fully-loaded pool should be a no-op (no error).
	if err := pool.WarmPool(poolSize); err != nil {
		t.Errorf("WarmPool no-op: unexpected error: %v", err)
	}

	// WarmPool with n > capacity should return an error.
	if err := pool.WarmPool(poolSize + 1); err == nil {
		t.Error("WarmPool(capacity+1): expected error, got nil")
	}
}
