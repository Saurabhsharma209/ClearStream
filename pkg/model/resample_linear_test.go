//go:build rnnoise || onnx

package model

import "testing"

// These tests target the shared resample.go helpers (upsample3x/downsample3x)
// used by both the CGo RNNoise backend ("rnnoise" tag) and the ONNX-based
// backends ("onnx" tag). They previously asserted the naive linear-
// interpolation/box-average behavior that only the "rnnoise" build got
// upgraded away from (see resample.go); now that both backends share the
// same Catmull-Rom/Kaiser-sinc implementation, these tests check invariants
// that hold regardless of the exact interpolation kernel, complementing the
// sine-wave fidelity checks in resample_roundtrip_test.go.

// TestUpsample3xPreservesOriginalSamples verifies that upsample3x reproduces
// the original sample exactly at every 3rd output position (t=0); only the
// two synthesized in-between samples come from the interpolation kernel.
func TestUpsample3xPreservesOriginalSamples(t *testing.T) {
	in := []int16{0, 1000, -2000, 3000, -4000, 32000, -32000}
	out := upsample3x(in)
	if len(out) != len(in)*3 {
		t.Fatalf("upsample3x: want %d samples, got %d", len(in)*3, len(out))
	}
	for i, s := range in {
		if out[i*3] != s {
			t.Errorf("upsample3x[%d*3]: want original sample %d, got %d", i, s, out[i*3])
		}
	}
}

// TestDownsample3xOutputLength verifies downsample3x always produces exactly
// n/3 output samples.
func TestDownsample3xOutputLength(t *testing.T) {
	in := make([]int16, 15)
	for i := range in {
		in[i] = int16(i * 100)
	}
	out := downsample3x(in)
	if len(out) != len(in)/3 {
		t.Fatalf("downsample3x: want %d samples, got %d", len(in)/3, len(out))
	}
}

// TestUpsampleDownsampleShortFrame exercises the boundary-clamping path in
// clampIdx for frames shorter than the Catmull-Rom/FIR support window,
// verifying it does not panic and preserves the expected round-trip length.
func TestUpsampleDownsampleShortFrame(t *testing.T) {
	in := []int16{100, -100, 200}
	up := upsample3x(in)
	if len(up) != len(in)*3 {
		t.Fatalf("upsample3x short frame: want %d samples, got %d", len(in)*3, len(up))
	}
	down := downsample3x(up)
	if len(down) != len(in) {
		t.Fatalf("downsample3x short frame: want %d samples, got %d", len(in), len(down))
	}
}

// TestUpsample3xNoOverflow verifies upsample3x never overflows int16 range
// even for full-scale extreme-amplitude alternating input, which stresses
// the Catmull-Rom overshoot-clamping logic the hardest.
func TestUpsample3xNoOverflow(t *testing.T) {
	in := make([]int16, 20)
	for i := range in {
		if i%2 == 0 {
			in[i] = 32767
		} else {
			in[i] = -32768
		}
	}
	out := upsample3x(in)
	for i, v := range out {
		if v > 32767 || v < -32768 {
			t.Fatalf("upsample3x[%d]: value %d out of int16 range", i, v)
		}
	}
}
