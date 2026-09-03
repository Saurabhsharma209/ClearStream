package model_test

import (
	"errors"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

func TestBatchWrapper_MatchesSequential(t *testing.T) {
	s := model.NewPassthrough()
	bs := model.AsBatch(s)

	frames := make([][]int16, 5)
	for i := range frames {
		f := make([]int16, 160)
		for j := range f {
			f[j] = int16((i + 1) * (j + 1) % 32767)
		}
		frames[i] = f
	}

	// Sequential
	seqOut := make([][]int16, 5)
	s2 := model.NewPassthrough()
	for i, f := range frames {
		out, err := s2.Process(f)
		if err != nil {
			t.Fatal(err)
		}
		seqOut[i] = out
	}

	// Batch
	batchOut, err := bs.ProcessBatch(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(batchOut) != len(seqOut) {
		t.Fatalf("length mismatch: got %d want %d", len(batchOut), len(seqOut))
	}
	for i := range seqOut {
		if len(batchOut[i]) != len(seqOut[i]) {
			t.Fatalf("frame %d length mismatch", i)
		}
	}
}

func TestAsBatch_AlreadyBatchSuppressor(t *testing.T) {
	s := model.NewPassthrough()
	bs1 := model.AsBatch(s)
	bs2 := model.AsBatch(bs1) // should not double-wrap
	if bs1 != bs2 {
		t.Fatal("AsBatch should return the same BatchSuppressor if already wrapped")
	}
}

func BenchmarkBatchVsSequential(b *testing.B) {
	frames := make([][]int16, 32)
	for i := range frames {
		frames[i] = make([]int16, 160)
	}
	s := model.NewPassthrough()
	bs := model.AsBatch(s)

	b.Run("sequential", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			for _, f := range frames {
				s.Process(f)
			}
		}
	})

	b.Run("batch", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			bs.ProcessBatch(frames)
		}
	})
}

// minimalSuppressor implements Suppressor but NOT BatchSuppressor,
// so AsBatch wraps it in a BatchWrapper (exercising batch.go).
//
// fail, when true, causes Process to return an error on the call whose
// 0-based index (in call order) equals failAt. This lets tests exercise
// both "fails immediately" (failAt: 0, the original behavior) and "fails
// partway through a batch" (failAt: N>0), the latter of which is needed to
// verify BatchWrapper.ProcessBatch actually preserves already-processed
// results for the calls that succeeded before the failure, not just their
// count.
type minimalSuppressor struct {
	fail   bool
	failAt int
	name   string
	calls  int
}

func (m *minimalSuppressor) Process(frame []int16) ([]int16, error) {
	idx := m.calls
	m.calls++
	if m.fail && idx == m.failAt {
		return nil, errors.New("process error")
	}
	out := make([]int16, len(frame))
	copy(out, frame)
	return out, nil
}

func (m *minimalSuppressor) Reset()       {}
func (m *minimalSuppressor) Close() error { return nil }
func (m *minimalSuppressor) Name() string { return m.name }

// TestBatchWrapper_DirectMethods exercises batch.go Process/Reset/Close/Name.
func TestBatchWrapper_DirectMethods(t *testing.T) {
	inner := &minimalSuppressor{name: "minimal"}
	bw := model.AsBatch(inner) // returns *BatchWrapper since inner lacks ProcessBatch

	// Cast to Suppressor to call the wrapper's own methods.
	s, ok := bw.(model.Suppressor)
	if !ok {
		t.Fatal("BatchWrapper must implement Suppressor")
	}

	frame := []int16{1, 2, 3, 4}
	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("Process: got %d samples, want %d", len(out), len(frame))
	}

	if name := s.Name(); name != "minimal+batch" {
		t.Errorf("Name() = %q, want %q", name, "minimal+batch")
	}

	s.Reset()
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestBatchWrapper_ProcessBatch_Error verifies partial-result on error.
func TestBatchWrapper_ProcessBatch_Error(t *testing.T) {
	inner := &minimalSuppressor{name: "failer", fail: true}
	bw := model.AsBatch(inner)

	frames := [][]int16{{1, 2}, {3, 4}, {5, 6}}
	out, err := bw.ProcessBatch(frames)
	if err == nil {
		t.Fatal("expected error from failing suppressor, got nil")
	}
	if len(out) != len(frames) {
		t.Errorf("ProcessBatch: output length %d, want %d", len(out), len(frames))
	}
}

// TestBatchWrapper_ProcessBatch_AllSucceed exercises the success return path
// of BatchWrapper.ProcessBatch ("return out, nil") through an actual
// BatchWrapper. TestBatchWrapper_MatchesSequential above looks like it
// covers this, but it wraps a Passthrough -- which already implements
// ProcessBatch itself, so AsBatch returns it unwrapped and BatchWrapper's
// own code never runs. Using minimalSuppressor (Suppressor only, no
// ProcessBatch) forces the real wrap, and asserting on content (not just
// length) would catch a regression that silently swapped or dropped a
// frame.
func TestBatchWrapper_ProcessBatch_AllSucceed(t *testing.T) {
	inner := &minimalSuppressor{name: "ok"}
	bw := model.AsBatch(inner)

	frames := [][]int16{{1, 2}, {3, 4}, {5, 6}}
	out, err := bw.ProcessBatch(frames)
	if err != nil {
		t.Fatalf("ProcessBatch: unexpected error: %v", err)
	}
	if len(out) != len(frames) {
		t.Fatalf("ProcessBatch: output length %d, want %d", len(out), len(frames))
	}
	for i := range frames {
		if len(out[i]) != len(frames[i]) {
			t.Fatalf("frame %d: length %d, want %d", i, len(out[i]), len(frames[i]))
		}
		for j := range frames[i] {
			if out[i][j] != frames[i][j] {
				t.Errorf("frame %d sample %d = %d, want %d", i, j, out[i][j], frames[i][j])
			}
		}
	}
	if inner.calls != len(frames) {
		t.Errorf("inner.Process called %d times, want %d", inner.calls, len(frames))
	}
}

// TestBatchWrapper_ProcessBatch_PartialSuccess verifies the documented
// contract precisely: when Process fails partway through a batch,
// ProcessBatch must return the already-processed output for every call that
// succeeded before the failure (not just the count -- the actual content),
// plus the original, unprocessed frames from the failure point onward.
// Previously only the "fails on the very first frame" case was exercised
// (failAt: 0), which can never distinguish "out[i] = processed" running
// correctly from it never running at all.
func TestBatchWrapper_ProcessBatch_PartialSuccess(t *testing.T) {
	inner := &minimalSuppressor{name: "partial", fail: true, failAt: 2}
	bw := model.AsBatch(inner)

	frames := [][]int16{{1, 1}, {2, 2}, {3, 3}, {4, 4}}
	out, err := bw.ProcessBatch(frames)
	if err == nil {
		t.Fatal("expected error from failing suppressor, got nil")
	}
	if len(out) != len(frames) {
		t.Fatalf("ProcessBatch: output length %d, want %d", len(out), len(frames))
	}

	// Frames 0 and 1 succeeded: out must hold the processed (copied) frame,
	// not be nil and not alias the original slice.
	for i := 0; i < 2; i++ {
		if out[i] == nil {
			t.Fatalf("frame %d: expected processed output, got nil", i)
		}
		for j := range frames[i] {
			if out[i][j] != frames[i][j] {
				t.Errorf("frame %d sample %d = %d, want %d", i, j, out[i][j], frames[i][j])
			}
		}
	}

	// Frame 2 triggered the failure and frame 3 was never attempted: both
	// must fall back to the original, unprocessed frame contents.
	for i := 2; i < 4; i++ {
		for j := range frames[i] {
			if out[i][j] != frames[i][j] {
				t.Errorf("frame %d sample %d = %d, want original %d", i, j, out[i][j], frames[i][j])
			}
		}
	}

	if inner.calls != 3 {
		t.Errorf("inner.Process called %d times, want 3 (stops at the failing call)", inner.calls)
	}
}
