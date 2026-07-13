package loadtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/loadtest"
)

// TestLoadTest_ContextCancelled covers the previously-0% early-exit branch
// in Run's per-frame loop: when ctx is already cancelled before Run starts
// processing frames, each session goroutine should break out of its frame
// loop immediately rather than processing all requested frames.
func TestLoadTest_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel up-front so ctx.Err() != nil on the very first check

	const sessions = 5
	const framesPerSession = 1000 // large enough that completing all of them would be observable

	start := time.Now()
	result := loadtest.Run(ctx, sessions, framesPerSession)
	elapsed := time.Since(start)

	if result.Sessions != sessions {
		t.Errorf("expected Sessions=%d, got %d", sessions, result.Sessions)
	}

	wantMaxFrames := uint64(sessions * framesPerSession)
	if result.Frames >= wantMaxFrames {
		t.Errorf("expected early cancellation to process far fewer than %d frames, got %d", wantMaxFrames, result.Frames)
	}

	// Sanity: with ctx already cancelled, Run should return almost immediately
	// (each goroutine should break on its first ctx.Err() check) rather than
	// grinding through all 1000 frames per session.
	if elapsed > 2*time.Second {
		t.Errorf("expected near-instant return on pre-cancelled context, took %v", elapsed)
	}
}

// TestLoadTest_ContextCancelledMidRun cancels the context concurrently while
// Run is in flight, exercising the same ctx.Err() break path but from a
// race-y angle (some sessions may complete a few frames before observing
// cancellation -- that's expected and fine).
func TestLoadTest_ContextCancelledMidRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()

	const sessions = 10
	const framesPerSession = 5000

	result := loadtest.Run(ctx, sessions, framesPerSession)

	wantMaxFrames := uint64(sessions * framesPerSession)
	if result.Frames >= wantMaxFrames {
		t.Errorf("expected mid-run cancellation to short-circuit before all %d frames, got %d", wantMaxFrames, result.Frames)
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors from cancellation path, got %d", result.Errors)
	}
}
