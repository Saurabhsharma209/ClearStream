package model

import "testing"

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
