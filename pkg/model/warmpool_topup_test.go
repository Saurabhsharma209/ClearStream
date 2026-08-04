package model

import "testing"

// TestWarmPoolTopsUpShortfallOnly is a regression test: WarmPool used to
// unconditionally drain (closing) every suppressor already sitting in the
// pool and then recreate n fresh ones from scratch on every call --
// discarding perfectly good, already-initialised suppressors even when the
// pool was only short by a couple, and (worse) leaving the pool at whatever
// partial count a mid-refill NewSuppressor failure happened to reach, since
// the drain had already thrown away the good ones before any replacements
// existed. WarmPool now only creates the shortfall and leaves existing ready
// suppressors untouched.
func TestWarmPoolTopsUpShortfallOnly(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 5)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	// Simulate a pool that has fallen short of capacity (e.g. a prior admin
	// action removed some suppressors) without violating WarmPool's
	// documented "call before any sessions begin" precondition -- drain 2
	// directly rather than via Acquire/Release, which represent in-flight
	// call-leg checkout.
	for i := 0; i < 2; i++ {
		s := <-pool.pool
		_ = s.Close()
	}
	if got := len(pool.pool); got != 3 {
		t.Fatalf("setup: expected 3 left in pool after draining 2, got %d", got)
	}

	if err := pool.WarmPool(5); err != nil {
		t.Fatalf("WarmPool(5): %v", err)
	}
	if got := len(pool.pool); got != 5 {
		t.Errorf("WarmPool(5) should top up the pool to 5, got %d", got)
	}

	// A pool that already meets the requested size must be a true no-op.
	if err := pool.WarmPool(3); err != nil {
		t.Fatalf("WarmPool(3) on an already-full pool: %v", err)
	}
	if got := len(pool.pool); got != 5 {
		t.Errorf("WarmPool(3) on a pool already holding 5 must not shrink it, got %d", got)
	}
}
