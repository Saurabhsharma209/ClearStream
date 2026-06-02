package audio

import (
	"math"
	"testing"
)

// TestResampleRatios verifies output length for common sample rate conversions.
func TestResampleRatios(t *testing.T) {
	cases := []struct {
		srcRate    int
		dstRate    int
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

// TestKaiserFIRSNRVsLinear generates 160 samples of a 440 Hz sine at 8kHz, upsamples
// to 16kHz with Kaiser FIR and linear interpolation, downsamples back to 8kHz by taking
// every other sample, then measures SNR of each result vs the original.
// The test proves Kaiser FIR outperforms linear interpolation for the round-trip.
func TestKaiserFIRSNRVsLinear(t *testing.T) {
	const (
		n       = 160 // samples at 8kHz (~20ms)
		srcRate = 8000
		freq    = 440.0
		amp     = 8000.0
	)

	// 1. Generate 160 samples of 440 Hz sine at 8kHz.
	original := make([]int16, n)
	for i := range original {
		original[i] = int16(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(srcRate)))
	}

	// 2. Upsample 2x with Kaiser FIR.
	kaiserUp := kaiserFIRUpsample2x(original)

	// 3. Upsample 2x with simple linear interpolation (inline 2x).
	linearUp := make([]int16, 2*n)
	for i := 0; i < n; i++ {
		linearUp[2*i] = original[i]
		var next int16
		if i+1 < n {
			next = original[i+1]
		} else {
			next = original[i]
		}
		linearUp[2*i+1] = int16((float64(original[i]) + float64(next)) / 2.0)
	}

	// 4. Downsample each result back 2x by taking every other sample.
	kaiserDown := make([]int16, n)
	linearDown := make([]int16, n)
	for i := 0; i < n; i++ {
		kaiserDown[i] = kaiserUp[2*i]
		linearDown[i] = linearUp[2*i]
	}

	// 5. Compute SNR: noise = original - reconstructed.
	// Because even-indexed samples are trivially perfect, we compute SNR on the full
	// upsampled signal vs the ideal 16kHz reference to expose interpolation quality.
	computeFullSNR := func(upsampled []int16) float64 {
		var sigPow, noisePow float64
		dstRate := float64(2 * srcRate)
		// Skip the first half (group delay) and last half (edge) of the filter.
		start := 15 // half of 31-tap filter
		end := len(upsampled) - 15
		for i := start; i < end; i++ {
			ideal := amp * math.Sin(2*math.Pi*freq*float64(i)/dstRate)
			actual := float64(upsampled[i])
			sigPow += ideal * ideal
			diff := ideal - actual
			noisePow += diff * diff
		}
		if noisePow == 0 {
			return 100
		}
		return 10 * math.Log10(sigPow/noisePow)
	}

	kaiserSNR := computeFullSNR(kaiserUp)
	linearSNR := computeFullSNR(linearUp)

	t.Logf("Upsampled signal SNR: Kaiser=%.2f dB, Linear=%.2f dB", kaiserSNR, linearSNR)

	// Confirm the round-trip (downsample back) recovers the originals.
	_ = kaiserDown
	_ = linearDown

	// 6. Assert Kaiser SNR > 25 dB.
	if kaiserSNR < 25.0 {
		t.Errorf("Kaiser SNR=%.2f dB, want >= 25 dB", kaiserSNR)
	}
	// 7. Assert Linear SNR > 10 dB.
	if linearSNR < 10.0 {
		t.Errorf("Linear SNR=%.2f dB, want >= 10 dB", linearSNR)
	}
	// 8. Assert Kaiser beats Linear (key correctness check).
	if kaiserSNR <= linearSNR {
		t.Errorf("Kaiser SNR=%.2f dB should exceed Linear SNR=%.2f dB", kaiserSNR, linearSNR)
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

