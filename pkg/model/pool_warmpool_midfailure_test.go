package model

import (
	"strings"
	"testing"
)

// TestWarmPoolMidLoopFailureLeavesPoolIntact covers WarmPool's NewSuppressor
// failure branch inside the topup loop (previously 0% covered). WarmPool was
// rewritten (see TestWarmPoolTopsUpShortfallOnly) specifically so that a
// failure partway through refilling a shortfall does not discard or corrupt
// suppressors the pool already held before the call -- but that guarantee was
// never actually exercised against a real NewSuppressor failure. This test
// forces one via an invalid config (deepfilter backend with no ModelPath)
// swapped in after construction, and asserts both the wrapped error message
// and that the pool's pre-existing suppressors survive untouched and the pool
// stays usable.
func TestWarmPoolMidLoopFailureLeavesPoolIntact(t *testing.T) {
	pool, err := NewSuppressorPool(passthroughCfg(), 5)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	// Drain 2 directly so the pool is short by 2, leaving 3 good suppressors
	// behind that must survive the failed topup below.
	for i := 0; i < 2; i++ {
		s := <-pool.pool
		_ = s.Close()
	}
	if got := len(pool.pool); got != 3 {
		t.Fatalf("setup: expected 3 left in pool after draining 2, got %d", got)
	}

	// Swap in a config that NewSuppressor is guaranteed to reject, forcing
	// WarmPool's topup loop to fail on its very first iteration.
	pool.cfg = SuppressorConfig{Backend: "deepfilter", ModelPath: ""}

	err = pool.WarmPool(5)
	if err == nil {
		t.Fatal("WarmPool(5) with a broken config: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "WarmPool init [1/2]") {
		t.Errorf("error message %q does not mention the expected init-index prefix", err.Error())
	}
	if !strings.Contains(err.Error(), "deepfilter requires ModelPath") {
		t.Errorf("error message %q does not wrap the underlying NewSuppressor error", err.Error())
	}

	// The 3 pre-existing suppressors must not have been discarded or closed
	// by the failed topup attempt.
	if got := len(pool.pool); got != 3 {
		t.Errorf("expected 3 pre-existing suppressors to survive the failed WarmPool, got %d", got)
	}

	// Restore a valid config so the pool remains usable for Acquire/Close.
	pool.cfg = passthroughCfg()
	if s := pool.Acquire(); s == nil {
		t.Error("Acquire() after failed WarmPool: expected a live suppressor, got nil")
	}
}
