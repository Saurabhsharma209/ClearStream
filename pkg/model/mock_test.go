package model

import (
	"testing"
)

func TestMockPassthrough(t *testing.T) {
	m := NewMockSuppressor() // gain=1.0
	input := []int16{0, 100, -200, 32767, -32768}
	out, err := m.Process(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, want := range input {
		if out[i] != want {
			t.Errorf("sample[%d]: want %d, got %d", i, want, out[i])
		}
	}
}

func TestMockGainHalf(t *testing.T) {
	m := &MockSuppressor{Gain: 0.5}
	input := []int16{1000, -2000, 4000}
	out, err := m.Process(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int16{500, -1000, 2000}
	for i, want := range expected {
		if out[i] != want {
			t.Errorf("sample[%d]: want %d, got %d", i, want, out[i])
		}
	}
}

func TestMockCallCounts(t *testing.T) {
	m := NewMockSuppressor()
	frame := []int16{1, 2, 3}

	m.Process(frame)
	m.Process(frame)
	m.Reset()
	m.Reset()
	m.Reset()

	if m.ProcessCalls != 2 {
		t.Errorf("ProcessCalls: want 2, got %d", m.ProcessCalls)
	}
	if m.ResetCalls != 3 {
		t.Errorf("ResetCalls: want 3, got %d", m.ResetCalls)
	}
}

func TestMockClipping(t *testing.T) {
	m := &MockSuppressor{Gain: 100.0}
	// Large positive and negative values
	input := []int16{1000, -1000}
	out, err := m.Process(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0] != 32767 {
		t.Errorf("positive clip: want 32767, got %d", out[0])
	}
	if out[1] != -32768 {
		t.Errorf("negative clip: want -32768, got %d", out[1])
	}
}

// TestMockSuppressor_ProcessBatch covers mock.go ProcessBatch.
func TestMockSuppressor_ProcessBatch(t *testing.T) {
	m := NewMockSuppressor()
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

// TestMockSuppressor_ProcessBatch_MultiFrame verifies ProcessBatch applies
// gain to every frame and increments ProcessCalls once per frame.
func TestMockSuppressor_ProcessBatch_MultiFrame(t *testing.T) {
	m := &MockSuppressor{Gain: 2.0}

	frames := [][]int16{
		{100, 200, 300},
		{-100, -200, -300},
		{0, 500, 1000},
	}
	out, err := m.ProcessBatch(frames)
	if err != nil {
		t.Fatalf("ProcessBatch: unexpected error: %v", err)
	}
	if len(out) != len(frames) {
		t.Fatalf("ProcessBatch: got %d frames, want %d", len(out), len(frames))
	}
	want0 := []int16{200, 400, 600}
	for i, v := range want0 {
		if out[0][i] != v {
			t.Errorf("frame[0][%d] = %d, want %d", i, out[0][i], v)
		}
	}
	want1 := []int16{-200, -400, -600}
	for i, v := range want1 {
		if out[1][i] != v {
			t.Errorf("frame[1][%d] = %d, want %d", i, out[1][i], v)
		}
	}
	if m.ProcessCalls != len(frames) {
		t.Errorf("ProcessCalls = %d, want %d", m.ProcessCalls, len(frames))
	}
}

// TestMockSuppressor_ProcessBatch_Empty verifies nil batch returns empty output.
func TestMockSuppressor_ProcessBatch_Empty(t *testing.T) {
	m := NewMockSuppressor()
	out, err := m.ProcessBatch(nil)
	if err != nil {
		t.Fatalf("ProcessBatch(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("ProcessBatch(nil): got %d frames, want 0", len(out))
	}
	if m.ProcessCalls != 0 {
		t.Errorf("ProcessCalls = %d, want 0", m.ProcessCalls)
	}
}

func TestMockSuppressor_Counters(t *testing.T) {
	m := NewMockSuppressor()
	frame := []int16{1, 2, 3}

	if m.ProcessCalls != 0 || m.ResetCalls != 0 {
		t.Fatalf("initial counters should be 0")
	}

	m.Process(frame)
	m.Process(frame)
	m.Reset()

	if m.ProcessCalls != 2 {
		t.Errorf("ProcessCalls: want 2, got %d", m.ProcessCalls)
	}
	if m.ResetCalls != 1 {
		t.Errorf("ResetCalls: want 1, got %d", m.ResetCalls)
	}
}
