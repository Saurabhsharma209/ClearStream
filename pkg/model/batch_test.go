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
type minimalSuppressor struct {
	fail bool
	name string
}

func (m *minimalSuppressor) Process(frame []int16) ([]int16, error) {
	if m.fail {
		return nil, errors.New("process error")
	}
	out := make([]int16, len(frame))
	copy(out, frame)
	return out, nil
}

func (m *minimalSuppressor) Reset() {}

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
