// Package file -- targets the ctx.Err() check inside ProcessWithOptions' NormalizePeak block (the "if opts.NormalizePeak { if err := ctx.Err(); err != nil { ... } }" branch), which coverage showed as never exercised.
//
// This check exists to abort promptly if the context is cancelled between
// decodeAndSuppress finishing and normalizePeakPCM starting, rather than
// paying for a needless peak-normalization pass on an already-cancelled
// request. Reaching it deterministically requires decodeAndSuppress (and
// the earlier top-of-function check) to observe ctx.Err() == nil, and only
// the *next* call to observe cancellation -- a timing window too narrow to
// hit reliably with a real context.CancelFunc fired from a goroutine. A
// small stub context that returns nil for its first N Err() calls and
// context.Canceled thereafter reproduces the window exactly.
package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// cancelAfterNErrCalls wraps a base context.Context and returns nil from
// Err() for the first n calls, then context.Canceled for every call after
// that. Done()/Deadline()/Value() are proxied to the base context
// unmodified, so exec.CommandContext's cancellation machinery (which only
// watches Done()) behaves as if the context were never cancelled -- this
// stub exists purely to control what ctx.Err() reports at specific call
// sites, not to actually kill any subprocess.
type cancelAfterNErrCalls struct {
	context.Context
	n     int32
	calls int32
}

func (c *cancelAfterNErrCalls) Err() error {
	if atomic.AddInt32(&c.calls, 1) > c.n {
		return context.Canceled
	}
	return nil
}

// TestProcessWithOptionsNormalizePeakCtxCancelledBeforeNormalize verifies
// that ProcessWithOptions aborts with context.Canceled -- without ever
// invoking normalizePeakPCM -- when the context is observed as cancelled
// specifically at the check guarding the NormalizePeak step, even though
// every earlier ctx.Err() check (top-of-function, end of decodeAndSuppress)
// saw a live context.
func TestProcessWithOptionsNormalizePeakCtxCancelledBeforeNormalize(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	// Calls to ctx.Err() before reaching the NormalizePeak block:
	//   1. ProcessWithOptions top-of-function check.
	//   2. decodeAndSuppress's end-of-decode check.
	// The 3rd call is the NormalizePeak block's own check -- let that one
	// (and any later one) report cancellation.
	ctx := &cancelAfterNErrCalls{Context: context.Background(), n: 2}

	p := newProcWithPath(ffmpeg)
	err := p.ProcessWithOptions(src, dst, Options{
		NormalizePeak: true,
		Context:       ctx,
	})
	if err == nil {
		t.Fatal("expected an error when ctx is cancelled before the normalize step, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got: %v", err)
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Errorf("expected no output file to be written when normalize is aborted by cancellation, but %s exists", dst)
	}
}
