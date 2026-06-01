package audio

import (
	"math"
	"testing"
)

// TestResampleRatios verifies output length for common sample rate conversions.
func TestResampleRatios(t *testing.T) {
	cases := []struct {
		srcRate  int
		dstRate  int
		numSamples int
	}{
		{8000, 16000, 800},  // 100ms at 8kHz → 1600 samples at 16kHz
		{16000, 8000, 1600}, // 100ms at 16kHz → 800 samples at 8kHz
		{8000, 8000, 800},   // passthrough
	}

	for _, tc := range cases {
		input := make([]int16, tc.numSamples)
		out, err := Resample(input, tc.srcRate, tc.dstRate)
		if err != nil {
			t.Errorf("Resample(%d→%d): unexpected error: %v", tc.srcRate, tc.dstRate, err)
			continue
		}
		ratio := float64(tc.dstRate) / float64(tc.srcRate)
		want := int(math.Round(float64(tc.numSamples) * ratio))
		got := len(out)
		// Allow ±1 sample rounding tolerance.
		if abs(got-want) > 1 {
			t.Errorf("Resample(%d→%d) len=%d, want %d (±1)", tc.srcRate, tc.dstRate, got, want)
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestKaiserVsLinearSNR generates a 440 Hz sine at 8kHz, upsamples to 16kHz with
// both the Kaiser FIR and linear interpolation methods, computes SNR vs the ideal
// 440 Hz reference, and asserts that Kaiser SNR exceeds linear SNR by ≥ 3 dB.
func TestKaiserVsLinearSNR(t *testing.T) {
	const (
		srcRate   = 8000
		dstRate   = 16000
		freq      = 440.0
		numFrames = 4000 // 500ms at 8kHz
	)

	// Generate 440 Hz sine at 8 kHz.
	input := make([]int16, numFrames)
	for i := range input {
		v := math.Sin(2 * math.Pi * freq * float64(i) / float64(srcRate))
		input[i] = int16(v * 16000) // moderate amplitude — well within int16 range
	}

	// Kaiser upsample (our high-quality path).
	kaiserOut := kaiserFIRUpsample2x(input)

	// Linear interpolation upsample.
	linearOut, err := linearResample(input, srcRate, dstRate)
	if err != nil {
		t.Fatalf("linearResample: %v", err)
	}

	kaiserSNR := computeSNR(kaiserOut, freq, dstRate)
	linearSNR := computeSNR(linearOut, freq, dstRate)

	t.Logf("Kaiser SNR = %.2f dB, Linear SNR = %.2f dB", kaiserSNR, linearSNR)

	if kaiserSNR <= linearSNR+3.0 {
		t.Errorf("expected Kaiser SNR > Linear SNR + 3dB, got kaiser=%.2f linear=%.2f",
			kaiserSNR, linearSNR)
	}
}

// computeSNR computes the SNR of samples vs an ideal sine at freq Hz and sampleRate.
// SNR = 10 * log10(signal_power / noise_power), where noise = (ideal - actual).
func computeSNR(samples []int16, freq, sampleRate float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	var sigPower, noisePower float64
	for i, s := range samples {
		ideal := math.Sin(2*math.Pi*freq*float64(i)/sampleRate) * 16000
		actual := float64(s)
		sigPower += ideal * ideal
		diff := ideal - actual
		noisePower += diff * diff
	}
	if noisePower == 0 {
		return 100 // perfect
	}
	return 10 * math.Log10(sigPower/noisePower)
}
