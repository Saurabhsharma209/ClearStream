package model_test

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

// TestSuppressorPoolReleaseNilDoesNotShrinkCapacity is a regression guard:
// SuppressorPool.Release used to enqueue a nil Suppressor into the pool
// channel whenever called with a nil argument on a still-open pool (rather
// than only guarding the already-closed path). Since Acquire only ever
// removes items and nothing ever replaces a stray nil slot, that permanently
// shrank the pool's effective capacity by one, invisibly, for the rest of
// its lifetime. Release(nil) must now be a true no-op on an open pool: the
// pool must still be able to satisfy exactly its original capacity worth of
// concurrent Acquire calls afterward.
func TestSuppressorPoolReleaseNilDoesNotShrinkCapacity(t *testing.T) {
	pool, err := model.NewSuppressorPool(model.SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	// Simulate a caller that ends up releasing a nil suppressor while the
	// pool is still open (e.g. a bug upstream, or a defensive defer that
	// fires even when Acquire happened to return nil for some other reason).
	pool.Release(nil)

	// The pool must still be able to hand out its full original capacity --
	// both real suppressors -- immediately, with no blocking.
	s1 := pool.Acquire()
	if s1 == nil {
		t.Fatal("Acquire() #1: expected a real Suppressor, got nil (pool capacity shrank)")
	}
	s2 := pool.Acquire()
	if s2 == nil {
		t.Fatal("Acquire() #2: expected a real Suppressor, got nil (pool capacity shrank -- Release(nil) leaked a slot)")
	}
	pool.Release(s1)
	pool.Release(s2)
}
