package model

import (
	"errors"
	"testing"
)

// errSuppressor fails Process on the Nth call (0-indexed).
type errSuppressor struct {
	failAt int
	calls  int
}

func (e *errSuppressor) Process(frame []int16) ([]int16, error) {
	if e.calls == e.failAt {
		e.calls++
		return nil, errors.New("deliberate error")
	}
	e.calls++
	out := make([]int16, len(frame))
	copy(out, frame)
	return out, nil
}
func (e *errSuppressor) Reset()       {}
func (e *errSuppressor) Close() error { return nil }
func (e *errSuppressor) Name() string { return "errsup" }

// TestBatchWrapper_ProcessBatch_ErrorPath exercises the early-exit path in
// BatchWrapper.ProcessBatch — the branch at 77.8%.
func TestBatchWrapper_ProcessBatch_ErrorPath(t *testing.T) {
	inner := &errSuppressor{failAt: 1} // fail on frame index 1
	bw := &BatchWrapper{s: inner}

	frames := [][]int16{
		{10, 20},
		{30, 40},
		{50, 60},
	}
	out, err := bw.ProcessBatch(frames)
	if err == nil {
		t.Fatal("expected error from ProcessBatch, got nil")
	}
	if len(out) != len(frames) {
		t.Fatalf("output length = %d, want %d", len(out), len(frames))
	}
	// out[0] should be processed (frame 0 succeeded).
	if out[0] == nil {
		t.Error("out[0] should be non-nil (was processed before error)")
	}
	// out[1] and out[2] should be original frames (set in error fallback path).
	for i := 1; i < len(frames); i++ {
		if out[i] == nil {
			t.Errorf("out[%d] should be original frame (non-nil)", i)
		}
	}
}

// TestBatchWrapper_Reset_propagates verifies Reset is forwarded to the inner suppressor.
func TestBatchWrapper_Reset_propagates(t *testing.T) {
	mock := NewMockSuppressor()
	bw := &BatchWrapper{s: mock}
	bw.Reset()
	if mock.ResetCalls != 1 {
		t.Errorf("ResetCalls = %d, want 1", mock.ResetCalls)
	}
}

// TestBatchWrapper_Close_nilError verifies Close forwards and returns nil for passthrough.
func TestBatchWrapper_Close_nilError(t *testing.T) {
	bw := &BatchWrapper{s: NewPassthrough()}
	if err := bw.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestBatchWrapper_Name_format verifies Name appends "+batch".
func TestBatchWrapper_Name_format(t *testing.T) {
	bw := &BatchWrapper{s: NewPassthrough()}
	if got := bw.Name(); got != "passthrough+batch" {
		t.Errorf("Name() = %q, want %q", got, "passthrough+batch")
	}
}
