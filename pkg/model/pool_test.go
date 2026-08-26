package model

import (
	"context"
	"errors"
	"strings"
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

// TestWarmPool_ExceedsCapacity covers the error path in WarmPool.
func TestWarmPool_ExceedsCapacity(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if err := pool.WarmPool(2); err != nil {
		t.Errorf("WarmPool(2): unexpected error: %v", err)
	}
	if err := pool.WarmPool(3); err == nil {
		t.Error("WarmPool(3) beyond capacity 2: expected error, got nil")
	}
}

// TestWarmPool_AlreadyFull covers the no-op branch (len(pool) >= n).
func TestWarmPool_AlreadyFull(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 4)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if err := pool.WarmPool(4); err != nil {
		t.Fatalf("WarmPool(4): %v", err)
	}
	// Already at capacity >= 2, should be a no-op.
	if err := pool.WarmPool(2); err != nil {
		t.Errorf("WarmPool(2) no-op: %v", err)
	}
}

// closingErrSuppressor is a Suppressor whose Close() always returns an error.
// Used to exercise the error-propagation path in SuppressorPool.Close().
type closingErrSuppressor struct {
	Passthrough
}

func (c *closingErrSuppressor) Close() error {
	return errors.New("deliberate close error")
}

func (c *closingErrSuppressor) Name() string { return "closingerr" }

// TestNewSuppressorPool_InitError covers the error path in NewSuppressorPool
// when NewSuppressor fails for one of the pool entries.
func TestNewSuppressorPool_InitError(t *testing.T) {
	cfg := SuppressorConfig{Backend: "rnnoise-onnx", ModelPath: ""}
	pool, err := NewSuppressorPool(cfg, 2)
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		t.Fatal("NewSuppressorPool with broken config: expected error, got nil")
	}
	if pool != nil {
		t.Errorf("expected nil pool on init error, got non-nil")
	}
}

// TestSuppressorPool_Close_ErrorPropagation exercises the firstErr path in Close()
// when a pooled suppressor's Close() returns an error.
func TestSuppressorPool_Close_ErrorPropagation(t *testing.T) {
	errSup := &closingErrSuppressor{}
	p := &SuppressorPool{
		pool: make(chan Suppressor, 1),
		cfg:  SuppressorConfig{Backend: "passthrough"},
		size: 1,
	}
	p.pool <- errSup

	if err := p.Close(); err == nil {
		t.Error("SuppressorPool.Close(): expected error from closingErrSuppressor, got nil")
	}
}

func TestSuppressorPool_CloseIdempotent(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Errorf("first Close() error: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close() panicked: %v", r)
		}
	}()
	pool.Close()
}

func TestSuppressorPool_ZeroSize(t *testing.T) {
	_, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 0)
	if err == nil {
		t.Error("expected error for pool size 0, got nil")
	}
}

func TestSuppressorPool_NegativeSize(t *testing.T) {
	_, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, -1)
	if err == nil {
		t.Error("expected error for pool size -1, got nil")
	}
}

func TestSuppressorPool_AcquireReleaseCycle(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	s1 := pool.Acquire()
	s2 := pool.Acquire()

	pool.Release(s1)
	s3 := pool.Acquire()
	if s3 == nil {
		t.Error("expected non-nil suppressor after release")
	}

	pool.Release(s2)
	pool.Release(s3)
}

// TestWarmPoolNoOpWhenFull verifies WarmPool is a no-op when pool already holds >= n items.
func TestWarmPoolNoOpWhenFull(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 3)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	// Pool is full (3 items). WarmPool(2) should succeed without changing anything.
	if err := pool.WarmPool(2); err != nil {
		t.Errorf("WarmPool(2) on full pool of 3: %v", err)
	}
	if pool.Size() != 3 {
		t.Errorf("Size() after no-op WarmPool = %d, want 3", pool.Size())
	}
}

// TestWarmPoolExceedsCapacity verifies WarmPool returns an error when n > pool capacity.
func TestWarmPoolExceedsCapacity(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if err := pool.WarmPool(3); err == nil {
		t.Error("WarmPool(3) on pool capacity 2: expected error, got nil")
	}
}

// TestWarmPoolRefill verifies WarmPool refills the pool after it has been drained.
func TestWarmPoolRefill(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	// Drain both slots.
	s1 := pool.Acquire()
	s2 := pool.Acquire()

	// Release them back.
	pool.Release(s1)
	pool.Release(s2)

	// WarmPool should drain-and-refill with 2 fresh suppressors.
	if err := pool.WarmPool(2); err != nil {
		t.Fatalf("WarmPool(2) after drain+release: %v", err)
	}

	// Should be able to acquire both without blocking.
	a := pool.Acquire()
	b := pool.Acquire()
	if a == nil || b == nil {
		t.Error("expected non-nil suppressors after WarmPool refill")
	}
	pool.Release(a)
	pool.Release(b)
}

// TestWarmPoolFromEmpty verifies WarmPool works when called with pool empty (no concurrent sessions).
func TestWarmPoolFromEmpty(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	// Drain the pool channel manually (simulate what would happen if items were acquired).
	<-pool.pool
	<-pool.pool

	// Now pool channel is empty. WarmPool(1) should refill 1 item.
	if err := pool.WarmPool(1); err != nil {
		t.Fatalf("WarmPool(1) from empty: %v", err)
	}

	// Acquire should not block.
	s := pool.Acquire()
	if s == nil {
		t.Error("expected non-nil suppressor after WarmPool(1)")
	}
	pool.Release(s)
}

// TestSuppressorPoolAcquireRelease verifies basic acquire/release for a passthrough pool.
func TestSuppressorPoolAcquireRelease(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 1)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	s := pool.Acquire()
	if s == nil {
		t.Fatal("Acquire() returned nil")
	}
	if s.Name() != "passthrough" {
		t.Errorf("Name() = %q, want passthrough", s.Name())
	}

	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(i)
	}
	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("Process: got %d samples, want %d", len(out), len(frame))
	}

	pool.Release(s)
}

// TestWarmPoolErrorMessage verifies the error message mentions capacity when exceeded.
func TestWarmPoolErrorMessage(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	err = pool.WarmPool(5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WarmPool") {
		t.Errorf("error message %q does not mention WarmPool", msg)
	}
}
