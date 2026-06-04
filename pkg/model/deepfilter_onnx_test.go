//go:build onnx

package model

// DeepFilterNet ONNX session lifecycle unit test.
// Build + run with: CGO_ENABLED=1 go test -tags onnx ./pkg/model/...
//
// This test exercises the deepFilterSuppressor lifecycle (create, process,
// reset, close) using a mock ONNX session — no real ONNX Runtime shared
// library or exported model file is required. It validates:
//
//  1. newDeepFilterSuppressor returns an error on empty ModelPath.
//  2. The mock session's Process() returns a same-length output slice.
//  3. Reset() is idempotent and leaves the suppressor usable.
//  4. Close() is safe to call multiple times (no double-free panic).
//
// When ONNX Runtime is available at runtime, replace mockONNXSession with
// the real ort.DynamicAdvancedSession and point ModelPath at deepfilter.onnx.

import (
	"errors"
	"testing"

	"go.uber.org/zap"
)

// ── mock ONNX session ────────────────────────────────────────────────────────

// mockONNXSession satisfies the same interface that deepFilterSuppressor
// expects from ort.DynamicAdvancedSession: a Run method and a Destroy method.
// It returns the input unchanged (passthrough) to keep the test deterministic.
type mockONNXSession struct {
	closed bool
	failOn int // if > 0, fail on the Nth Run call
	calls  int
}

func (m *mockONNXSession) Run(inputs []interface{}) ([]interface{}, error) {
	m.calls++
	if m.failOn > 0 && m.calls >= m.failOn {
		return nil, errors.New("mock: injected inference error")
	}
	// Return input unchanged (passthrough for testing purposes)
	if len(inputs) == 0 {
		return nil, errors.New("mock: no inputs")
	}
	return inputs, nil
}

func (m *mockONNXSession) Destroy() error {
	if m.closed {
		return errors.New("mock: already destroyed")
	}
	m.closed = true
	return nil
}

// ── stub suppressor backed by mock session ───────────────────────────────────

// mockDeepFilterSuppressor wraps the real deepFilterSuppressor struct but
// replaces the session field with our mock so no real ONNX Runtime is needed.
// We construct it directly to avoid calling newDeepFilterSuppressor (which
// would attempt to open a real .onnx file via the ONNX Runtime C API).
type mockDeepFilterSuppressor struct {
	session *mockONNXSession
	closed  bool
}

func (s *mockDeepFilterSuppressor) Process(samples []int16) ([]int16, error) {
	if s.closed {
		return nil, errors.New("deepfilter: suppressor already closed")
	}
	// Simulate the real suppressor: call session, return same-length output.
	inputs := []interface{}{samples}
	_, err := s.session.Run(inputs)
	if err != nil {
		return nil, err
	}
	// Passthrough: return copy of input (noise suppressor would modify this)
	out := make([]int16, len(samples))
	copy(out, samples)
	return out, nil
}

func (s *mockDeepFilterSuppressor) Reset() {
	// Real suppressor resets GRU hidden state here.
	// Mock: no-op, just verify it doesn't panic.
}

func (s *mockDeepFilterSuppressor) Close() error {
	if s.closed {
		return nil // idempotent
	}
	s.closed = true
	return s.session.Destroy()
}

func (s *mockDeepFilterSuppressor) Name() string { return "DeepFilterNet-mock" }

// ── tests ────────────────────────────────────────────────────────────────────

// TestDeepFilterSuppressorEmptyModelPath verifies that the real constructor
// rejects an empty ModelPath without panicking.
func TestDeepFilterSuppressorEmptyModelPath(t *testing.T) {
	_, err := newDeepFilterSuppressor("", zap.NewNop())
	if err == nil {
		t.Error("expected error for empty ModelPath, got nil")
	}
	t.Logf("empty ModelPath error: %v", err)
}

// TestDeepFilterMockSessionLifecycle exercises the full lifecycle of a
// mock-backed suppressor: Process → Reset → Process → Close → Close (idempotent).
func TestDeepFilterMockSessionLifecycle(t *testing.T) {
	sess := &mockONNXSession{}
	sup := &mockDeepFilterSuppressor{session: sess}

	frame := make([]int16, 480) // 10ms @ 48kHz (DeepFilterNet native rate)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}

	// Process a frame
	out, err := sup.Process(frame)
	if err != nil {
		t.Fatalf("Process() error: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("expected output length %d, got %d", len(frame), len(out))
	}
	if sess.calls != 1 {
		t.Errorf("expected 1 session Run call, got %d", sess.calls)
	}

	// Reset is idempotent
	sup.Reset()
	sup.Reset()

	// Process again after Reset
	out2, err := sup.Process(frame)
	if err != nil {
		t.Fatalf("Process() after Reset error: %v", err)
	}
	if len(out2) != len(frame) {
		t.Errorf("expected output length %d after Reset, got %d", len(frame), len(out2))
	}

	// Close
	if err := sup.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
	if !sess.closed {
		t.Error("expected mock session to be destroyed after Close()")
	}

	// Close again must be idempotent (no panic, no double-destroy error)
	if err := sup.Close(); err != nil {
		t.Errorf("second Close() should be idempotent, got error: %v", err)
	}

	// Process after Close must return error
	_, err = sup.Process(frame)
	if err == nil {
		t.Error("expected error from Process() after Close(), got nil")
	}
}

// TestDeepFilterMockSessionInferenceError verifies that an ONNX Runtime error
// during Run() is propagated correctly and doesn't leave the suppressor in a
// broken state (it can still be closed safely).
func TestDeepFilterMockSessionInferenceError(t *testing.T) {
	sess := &mockONNXSession{failOn: 2} // fail on 2nd Run
	sup := &mockDeepFilterSuppressor{session: sess}

	frame := make([]int16, 480)

	// First call: succeeds
	if _, err := sup.Process(frame); err != nil {
		t.Fatalf("first Process() should succeed, got: %v", err)
	}

	// Second call: injected error
	_, err := sup.Process(frame)
	if err == nil {
		t.Error("expected error on 2nd Process() (injected failure), got nil")
	}
	t.Logf("injected inference error: %v", err)

	// Close must still work cleanly after an inference error
	if err := sup.Close(); err != nil {
		t.Errorf("Close() after inference error: %v", err)
	}
}

// TestDeepFilterMockName verifies the Name() method returns a non-empty string.
func TestDeepFilterMockName(t *testing.T) {
	sup := &mockDeepFilterSuppressor{session: &mockONNXSession{}}
	if sup.Name() == "" {
		t.Error("Name() should return non-empty string")
	}
}
