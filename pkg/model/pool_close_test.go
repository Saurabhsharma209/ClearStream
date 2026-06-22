package model

import "testing"

// TestSuppressorPool_Close_AllReleased verifies Close drains all returned suppressors.
func TestSuppressorPool_Close_AllReleased(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 3)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	// All suppressors are in the pool — Close should iterate and close them all.
	if err := pool.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestSuppressorPool_Close_PoolEmpty verifies Close works when pool is drained
// (all suppressors have been Acquired but not Released).
func TestSuppressorPool_Close_PoolEmpty(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	// Acquire both — pool channel is now empty.
	s1 := pool.Acquire()
	s2 := pool.Acquire()
	_ = s1
	_ = s2

	// Close on an empty pool should close the channel without error.
	if err := pool.Close(); err != nil {
		t.Errorf("Close() on empty pool = %v, want nil", err)
	}
}

// TestSuppressorPool_Close_Idempotent2 exercises closeOnce — second call is a no-op.
func TestSuppressorPool_Close_Idempotent2(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 1)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Errorf("first Close(): %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close() panicked: %v", r)
		}
	}()
	_ = pool.Close() // must not panic
}
