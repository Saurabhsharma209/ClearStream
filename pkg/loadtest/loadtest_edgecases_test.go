package loadtest_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/loadtest"
)

// TestLoadTestZeroSessions covers the previously-untested sessions=0 edge case.
// With no sessions, Run must return a clean zero Result -- no goroutines are
// spawned, wg.Wait() returns immediately, and FPS must not become NaN/Inf
// (which float64(0)/(0/1000) division could theoretically produce if the
// elapsed-time denominator were ever exactly zero). This guards against a
// regression where a caller passes a dynamically-computed session count
// (e.g. from a config value or CLI flag) that happens to be zero.
func TestLoadTestZeroSessions(t *testing.T) {
	ctx := context.Background()
	result := loadtest.Run(ctx, 0, 100)

	if result.Sessions != 0 {
		t.Errorf("expected Sessions=0, got %d", result.Sessions)
	}
	if result.Frames != 0 {
		t.Errorf("expected Frames=0, got %d", result.Frames)
	}
	if result.Errors != 0 {
		t.Errorf("expected Errors=0, got %d", result.Errors)
	}
	if math.IsNaN(result.FPS) || math.IsInf(result.FPS, 0) {
		t.Errorf("FPS must be a finite number for zero sessions, got %v", result.FPS)
	}
}

// TestLoadTestZeroFrames covers frames=0: sessions are still spawned (each
// pipeline is constructed) but the per-session frame loop never executes.
// This must complete quickly with zero frames/errors and a finite FPS, not
// hang or divide-by-zero.
func TestLoadTestZeroFrames(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	result := loadtest.Run(ctx, 5, 0)
	elapsed := time.Since(start)

	if result.Sessions != 5 {
		t.Errorf("expected Sessions=5, got %d", result.Sessions)
	}
	if result.Frames != 0 {
		t.Errorf("expected Frames=0 for frames=0, got %d", result.Frames)
	}
	if math.IsNaN(result.FPS) || math.IsInf(result.FPS, 0) {
		t.Errorf("FPS must be a finite number for zero frames, got %v", result.FPS)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected near-instant return for frames=0, took %v", elapsed)
	}
}

// TestLoadTestNegativeSessionsPanics documents a known gap: Run does not
// validate the sessions argument before using it to size a buffered channel
// (sem := make(chan struct{}, sessions)). A negative sessions count causes
// an unrecoverable runtime panic ("makechan: size out of range") instead of
// a graceful error or zero Result.
//
// This is out of the QA/Testing workstream's file scope to fix (pkg/loadtest/
// loadtest.go is production code), so it is flagged here as a pinned
// regression/documentation test rather than silently left uncovered. If a
// future change adds input validation to Run, this test should be updated
// to assert the new graceful behavior instead of the panic.
func TestLoadTestNegativeSessionsPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected Run(ctx, -1, ...) to panic given current unvalidated " +
				"make(chan struct{}, sessions) call -- if this no longer panics, " +
				"pkg/loadtest/loadtest.go has added input validation; update this " +
				"test to assert the new graceful-error behavior instead")
		}
		t.Logf("confirmed known gap: Run panicked with negative sessions: %v", r)
	}()

	ctx := context.Background()
	_ = loadtest.Run(ctx, -1, 10)
}
