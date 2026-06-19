package model

import (
	"strings"
	"testing"
)

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
