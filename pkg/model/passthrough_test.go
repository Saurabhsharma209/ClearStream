package model

import (
	"testing"
)

// TestPassthroughProcessBatch verifies ProcessBatch preserves frame order and content.
func TestPassthroughProcessBatch(t *testing.T) {
	p := NewPassthrough()

	frames := [][]int16{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	}

	out, err := p.ProcessBatch(frames)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(out) != len(frames) {
		t.Fatalf("ProcessBatch: got %d frames, want %d", len(out), len(frames))
	}
	for i, frame := range frames {
		if len(out[i]) != len(frame) {
			t.Errorf("frame[%d]: got len %d, want %d", i, len(out[i]), len(frame))
			continue
		}
		for j, v := range frame {
			if out[i][j] != v {
				t.Errorf("frame[%d][%d] = %d, want %d", i, j, out[i][j], v)
			}
		}
	}
}

// TestPassthroughProcessBatchEmpty verifies ProcessBatch handles an empty batch.
func TestPassthroughProcessBatchEmpty(t *testing.T) {
	p := NewPassthrough()
	out, err := p.ProcessBatch(nil)
	if err != nil {
		t.Fatalf("ProcessBatch(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("ProcessBatch(nil): got %d frames, want 0", len(out))
	}
}

// TestPassthroughProcessZeroCopy verifies that Process returns the input slice directly
// (zero-copy: no allocation, output aliases input).
func TestPassthroughProcessZeroCopy(t *testing.T) {
	p := NewPassthrough()
	frame := []int16{1, 2, 3}
	out, err := p.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	// Zero-copy: output must have the same backing array as input.
	if len(out) != len(frame) {
		t.Fatalf("Process: got len %d, want %d", len(out), len(frame))
	}
	for i, v := range frame {
		if out[i] != v {
			t.Errorf("Process: out[%d] = %d, want %d", i, out[i], v)
		}
	}
	// Confirm alias: mutating out[0] also mutates frame[0].
	out[0] = 99
	if frame[0] != 99 {
		t.Errorf("expected zero-copy alias: frame[0] = %d, want 99", frame[0])
	}
}

// TestPassthroughResetAllowsSubsequentProcess verifies Reset then Process works correctly.
func TestPassthroughResetAllowsSubsequentProcess(t *testing.T) {
	p := NewPassthrough()
	p.Reset()
	p.Reset() // multiple resets should be fine

	frame := []int16{1, 2, 3, 4, 5}
	out, err := p.Process(frame)
	if err != nil {
		t.Fatalf("Process after Reset: %v", err)
	}
	if len(out) != len(frame) {
		t.Fatalf("Process after Reset: got len %d, want %d", len(out), len(frame))
	}
	for i, v := range frame {
		if out[i] != v {
			t.Errorf("Process after Reset: out[%d] = %d, want %d", i, out[i], v)
		}
	}
}
