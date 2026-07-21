package model

import "testing"

// TestAggressiveness_DeadWiringRegression proves the historical bug: before
// this file existed, SuppressorConfig.Aggressiveness was documented,
// populated by every NoiseProfile (see profile.go), and even asserted on in
// profile_test.go -- but NewSuppressor never passed it to any backend
// constructor and no backend constructor accepted or used it, so it had zero
// effect on any processed frame. blendAggressiveness is the fix: it is the
// shared, tag-free primitive that every real backend (RNNoise CGo,
// RNNoise-ONNX, DeepFilterNet ONNX) now calls with the frame's configured
// level. This test exercises that primitive directly, independent of build
// tags, so it runs under the default `go test ./pkg/model/...`.
func TestAggressiveness_DeadWiringRegression(t *testing.T) {
	original := []int16{1000, -1000, 32000, -32000, 0}
	// "processed" simulates a backend's full-strength (fully suppressed)
	// output: attenuated toward silence relative to original.
	processed := []int16{100, -100, 3200, -3200, 0}

	tests := []struct {
		name  string
		level int
		want  []int16
	}{
		{
			name:  "level 0 (unset/backend default) returns processed unchanged",
			level: 0,
			want:  processed,
		},
		{
			name:  "level 3 (aggressive) returns processed unchanged",
			level: 3,
			want:  processed,
		},
		{
			name:  "level 1 (mild) blends 40% processed / 60% original",
			level: 1,
			want:  []int16{640, -640, 20480, -20480, 0},
		},
		{
			name:  "level 2 (medium) blends 70% processed / 30% original",
			level: 2,
			want:  []int16{370, -370, 11840, -11840, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := blendAggressiveness(original, processed, tc.level)
			if len(got) != len(tc.want) {
				t.Fatalf("length: got %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %d, want %d", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestAggressiveness_MildIsLessAggressiveThanMedium verifies the core
// engineering property that "mild" (1) leaves more of the original signal's
// energy intact than "medium" (2) -- i.e. increasing the level actually
// increases suppression strength, rather than being a no-op level for every
// value as it silently was before this fix.
func TestAggressiveness_MildIsLessAggressiveThanMedium(t *testing.T) {
	original := []int16{10000}
	processed := []int16{0} // fully suppressed to silence

	mild := blendAggressiveness(original, processed, 1)
	medium := blendAggressiveness(original, processed, 2)

	if mild[0] <= medium[0] {
		t.Errorf("expected mild (level 1) to retain more original-signal energy than medium (level 2): mild=%d medium=%d", mild[0], medium[0])
	}
}

// TestAggressiveness_MismatchedLengthsReturnsProcessed guards against
// index-out-of-range panics if a backend ever hands blendAggressiveness two
// differently-sized frames.
func TestAggressiveness_MismatchedLengthsReturnsProcessed(t *testing.T) {
	original := []int16{1, 2, 3}
	processed := []int16{9, 8}
	got := blendAggressiveness(original, processed, 1)
	if len(got) != len(processed) {
		t.Fatalf("expected processed frame returned unchanged on length mismatch, got len %d", len(got))
	}
	for i := range processed {
		if got[i] != processed[i] {
			t.Errorf("index %d: got %d, want %d (unchanged processed)", i, got[i], processed[i])
		}
	}
}

// TestAggressiveness_OutOfRangeLevelFallsBackToFullStrength ensures an
// unexpected/out-of-range Aggressiveness value degrades safely to the
// documented "0=backend default" behavior instead of panicking or silently
// mis-blending.
func TestAggressiveness_OutOfRangeLevelFallsBackToFullStrength(t *testing.T) {
	original := []int16{5000}
	processed := []int16{500}
	got := blendAggressiveness(original, processed, 99)
	if got[0] != processed[0] {
		t.Errorf("out-of-range level: got %d, want processed value %d unchanged", got[0], processed[0])
	}
}

// TestNewSuppressor_RNNoiseDefaultPassesAggressivenessWithoutError is a
// build-tag-independent smoke test proving NewSuppressor's "rnnoise"/""
// branch compiles and runs successfully after threading cfg.Aggressiveness
// into NewRNNoise -- under the default (no build tag) test run this resolves
// to the passthrough fallback (rnnoise_nocgo.go), which must still accept
// the argument without error.
func TestNewSuppressor_RNNoiseDefaultPassesAggressivenessWithoutError(t *testing.T) {
	s, err := NewSuppressor(SuppressorConfig{Backend: "rnnoise", Aggressiveness: 2})
	if err != nil {
		t.Fatalf("NewSuppressor(rnnoise, Aggressiveness=2): unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("NewSuppressor(rnnoise, Aggressiveness=2): returned nil suppressor")
	}
	defer s.Close()
}
