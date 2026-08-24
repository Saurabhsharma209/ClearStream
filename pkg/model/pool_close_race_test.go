package model

import (
	"sync"
	"testing"
	"time"
)

// TestSuppressorPool_ReleaseDuringClose_NoPanic is a regression test for a
// TOCTOU race between Release and Close: Release used to check p.closed and
// then, on a separate unsynchronized step, send the Suppressor back on
// p.pool. If Close ran to completion in the window between Release's check
// and its send, Release would panic with "send on closed channel".
func TestSuppressorPool_ReleaseDuringClose_NoPanic(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 4)
		if err != nil {
			t.Fatalf("NewSuppressorPool: %v", err)
		}

		var wg sync.WaitGroup
		stop := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					s := pool.Acquire()
					if s != nil {
						pool.Release(s)
					}
				}
			}()
		}

		time.Sleep(200 * time.Microsecond)
		if err := pool.Close(); err != nil {
			t.Errorf("iter %d: Close() = %v, want nil", iter, err)
		}
		close(stop)
		wg.Wait()
	}
}
