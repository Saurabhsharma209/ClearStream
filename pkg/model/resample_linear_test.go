//go:build onnx

package model

import (
	"testing"
)

func TestUpsample3xLinearInterpolation(t *testing.T) {
	in := []int16{0, 3000}
	out := upsample3x(in)
	if len(out) != len(in)*3 {
		t.Fatalf("upsample3x: want %d samples, got %d", len(in)*3, len(out))
	}
	want := []int16{0, 1000, 2000, 3000, 3000, 3000}
	for i, w := range want {
		if out[i] != w {
			t.Errorf("upsample3x[%d]: want %d, got %d", i, w, out[i])
		}
	}
}

func TestDownsample3xAveraging(t *testing.T) {
	in := []int16{0, 1000, 2000, 3000, 4000, 5000}
	out := downsample3x(in)
	if len(out) != len(in)/3 {
		t.Fatalf("downsample3x: want %d samples, got %d", len(in)/3, len(out))
	}
	want := []int16{1000, 4000}
	for i, w := range want {
		if out[i] != w {
			t.Errorf("downsample3x[%d]: want %d, got %d", i, w, out[i])
		}
	}
}
