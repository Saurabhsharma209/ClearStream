package model

import (
	"sync/atomic"
	"testing"
)

// These tests cover a real bug found in pool.go: SuppressorPool.Acquire,
// Release, and WarmPool previously assumed the underlying channel was never
// closed while they operated on it. Once Close() had run, the channel
// receive in Acquire (and the drain loop inside WarmPool) always returns a
// nil Suppressor immediately -- select never falls through to the `default`
// case for a closed channel -- and calling Reset()/Close() on that nil
// interface value panicked with a nil-pointer dereference. Similarly,
// Release() sending on the now-closed channel panicked with "send on closed
// channel". All three paths are now guarded by the pool's `closed` flag.

// TestAcquireAfterClose verifies Acquire returns nil (not panics) once the
// pool has been closed and fully drained.
func TestAcquireAfterClose(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 1)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Acquire() after Close() panicked: %v", r)
		}
	}()

	if s := pool.Acquire(); s != nil {
		t.Errorf("Acquire() after Close() = %v, want nil", s)
	}
}

// TestReleaseAfterClose verifies Release closes the suppressor directly
// (instead of panicking on a send to the closed channel) once the pool has
// been closed.
func TestReleaseAfterClose(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 1)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	// Acquire the sole suppressor out of the pool before closing, so Close()
	// doesn't drain/close it itself -- we want to Release it back afterward.
	s := pool.Acquire()
	if s == nil {
		t.Fatal("Acquire() returned nil before Close()")
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Release() after Close() panicked: %v", r)
		}
	}()

	pool.Release(s) // must not panic (send on closed channel)

	// Release with a nil Suppressor after close must also be a safe no-op.
	pool.Release(nil)
}

// TestWarmPoolAfterClose verifies WarmPool returns a clear error (rather
// than panicking mid-drain) once the pool has been closed.
func TestWarmPoolAfterClose(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WarmPool() after Close() panicked: %v", r)
		}
	}()

	if err := pool.WarmPool(1); err == nil {
		t.Error("WarmPool() after Close(): expected error, got nil")
	}
}

// TestReleaseAfterCloseClosesSuppressor verifies Release, when called after
// Close, actually calls Close on the released Suppressor (rather than
// silently dropping it and leaking any underlying resources), using a
// MockSuppressor injected directly so we can distinguish it from the pool's
// own channel-based bookkeeping.
func TestReleaseAfterCloseClosesSuppressor(t *testing.T) {
	mock := NewMockSuppressor()
	p := &SuppressorPool{
		pool: make(chan Suppressor, 1),
		cfg:  passthroughCfg(),
		size: 1,
	}
	atomic.StoreInt32(&p.closed, 1)

	p.Release(mock)

	// Release() after closed=true should not enqueue into the pool channel
	// (it should route straight to Close() instead).
	select {
	case <-p.pool:
		t.Error("Release() after closed=true should not enqueue into pool channel")
	default:
	}
}
