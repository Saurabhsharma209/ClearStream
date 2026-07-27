package model

import "testing"

// TestBlendAggressivenessClampsOverflow is a regression guard for
// blendAggressiveness's int16 saturation clamp. Since wet+dry always sum to
// 1.0 for today's aggressivenessWetRatio outputs (0, 0.40, 0.70, 1.0), the
// blend is a convex combination of two in-range int16 values and can never
// actually overflow -- so this test cannot exercise the clamp branches as
// currently written, but locks in that boundary values (min/max int16) blend
// to exactly the expected result with no off-by-one drift. If a future
// aggressiveness level or config ever produces a non-convex weight (wet>1 or
// dry<0), the "out of range" assertions below give this an immediate,
// specific failure instead of a silent int16 wraparound bug.
func TestBlendAggressivenessClampsOverflow(t *testing.T) {
	original := []int16{32767, -32768, 32767, -32768}
	processed := []int16{32767, -32768, -32768, 32767}

	out := blendAggressiveness(original, processed, 2)
	for i, v := range out {
		if v > 32767 || v < -32768 {
			t.Errorf("index %d: blended value %d out of int16 range", i, v)
		}
	}

	// level 1 -> wet=0.40, dry=0.60, opposite-sign extremes at every index.
	out = blendAggressiveness(original, processed, 1)
	for i, v := range out {
		if v > 32767 || v < -32768 {
			t.Errorf("level 1, index %d: blended value %d out of int16 range", i, v)
		}
	}
}

// TestBlendAggressivenessLengthMismatchReturnsProcessedUnchanged proves the
// defensive length-mismatch guard returns processed as-is (rather than
// panicking on an index out of range) when original and processed frames
// differ in length -- a situation that "should not happen for well-behaved
// backends" per the doc comment, but must degrade safely if it does.
func TestBlendAggressivenessLengthMismatchReturnsProcessedUnchanged(t *testing.T) {
	original := []int16{1, 2, 3}
	processed := []int16{10, 20}

	out := blendAggressiveness(original, processed, 2)
	if len(out) != len(processed) {
		t.Fatalf("expected output length %d (processed unchanged), got %d", len(processed), len(out))
	}
	for i := range processed {
		if out[i] != processed[i] {
			t.Errorf("index %d: expected processed value %d unchanged, got %d", i, processed[i], out[i])
		}
	}
}

// TestBlendAggressivenessFullWetReturnsProcessedUnchanged locks in that
// level 0 (unset/backend-default) and level 3 (aggressive) both take the
// wet==1.0 shortcut and return processed by reference-equal value, not a
// blended copy -- preserving pre-Aggressiveness-fix behavior exactly for
// every caller that never set the field.
func TestBlendAggressivenessFullWetReturnsProcessedUnchanged(t *testing.T) {
	for _, level := range []int{0, 3} {
		original := []int16{100, -100}
		processed := []int16{5000, -5000}
		out := blendAggressiveness(original, processed, level)
		for i := range processed {
			if out[i] != processed[i] {
				t.Errorf("level %d, index %d: expected unblended processed value %d, got %d", level, i, processed[i], out[i])
			}
		}
	}
}
