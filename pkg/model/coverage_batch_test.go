package model_test

import (
	"errors"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

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

// TestPassthrough_ResetAndProcessBatch covers the 0% lines in passthrough.go.
func TestPassthrough_ResetAndProcessBatch(t *testing.T) {
	p := model.NewPassthrough()
	p.Reset() // no-op; verify no panic

	frames := [][]int16{{10, 20, 30}, {40, 50, 60}}
	out, err := p.ProcessBatch(frames)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(out) != len(frames) {
		t.Fatalf("ProcessBatch: got %d frames, want %d", len(out), len(frames))
	}
}

// TestMockSuppressor_ProcessBatch covers mock.go ProcessBatch.
func TestMockSuppressor_ProcessBatch(t *testing.T) {
	m := model.NewMockSuppressor()
	m.Gain = 2.0

	frames := [][]int16{{100, 200}, {300, 400}}
	out, err := m.ProcessBatch(frames)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if out[0][0] != 200 || out[0][1] != 400 {
		t.Errorf("ProcessBatch gain: got %v, want [200 400]", out[0])
	}
}

// TestWarmPool_ExceedsCapacity covers the error path in WarmPool.
func TestWarmPool_ExceedsCapacity(t *testing.T) {
	pool, err := model.NewSuppressorPool(model.SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if err := pool.WarmPool(2); err != nil {
		t.Errorf("WarmPool(2): unexpected error: %v", err)
	}
	if err := pool.WarmPool(3); err == nil {
		t.Error("WarmPool(3) beyond capacity 2: expected error, got nil")
	}
}

// TestWarmPool_AlreadyFull covers the no-op branch (len(pool) >= n).
func TestWarmPool_AlreadyFull(t *testing.T) {
	pool, err := model.NewSuppressorPool(model.SuppressorConfig{Backend: "passthrough"}, 4)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	if err := pool.WarmPool(4); err != nil {
		t.Fatalf("WarmPool(4): %v", err)
	}
	// Already at capacity >= 2, should be a no-op.
	if err := pool.WarmPool(2); err != nil {
		t.Errorf("WarmPool(2) no-op: %v", err)
	}
}
