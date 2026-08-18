package audio

import "testing"

// TestNormalize_MinInt16PeakOverflow is a regression test for a peak-detection
// overflow in Normalize: negating an int16 sample equal to math.MinInt16
// (-32768) in place ("s = -s") overflows two's-complement int16 arithmetic
// and silently stays -32768 instead of becoming +32768. Since the running
// `peak` accumulator only ever compares positive values ("s > peak" with
// peak starting at 0), a buffer whose loudest sample is exactly -32768 never
// updates peak away from 0, so Normalize's "peak == 0" guard incorrectly
// treats an already-maximally-loud buffer as silence and returns it
// unchanged -- the opposite of what a clipping-prevention function is
// documented to do.
func TestNormalize_MinInt16PeakOverflow(t *testing.T) {
	samples := []int16{-32768, 100, -50, 200}
	maxAbs := int16(16000)

	out := Normalize(samples, maxAbs)

	for i, v := range out {
		if int(v) > int(maxAbs) || int(v) < -int(maxAbs) {
			t.Errorf("Normalize sample[%d] = %d, exceeds maxAbs=%d after normalization", i, v, maxAbs)
		}
	}

	// The most direct symptom: with the overflow bug, peak stays 0 and
	// Normalize returns the input completely untouched (out[0] == -32768,
	// unscaled), even though -32768 clearly exceeds maxAbs=16000.
	if out[0] == -32768 {
		t.Errorf("Normalize did not scale a buffer containing MinInt16 (-32768): got out[0]=%d, want a value scaled down to within +/-%d", out[0], maxAbs)
	}
}
