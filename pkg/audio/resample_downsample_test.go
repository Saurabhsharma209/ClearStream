package audio

import (
	"math"
	"testing"
)

// TestKaiserFIRDownsample2xLength verifies that kaiserFIRDownsample2x returns
// ceil(len(input)/2) samples for both even- and odd-length inputs.
func TestKaiserFIRDownsample2xLength(t *testing.T) {
	cases := []struct {
		inputLen int
		wantLen  int
	}{
		{800, 400},  // even
		{801, 401},  // odd
		{1600, 800}, // even — 100ms at 16kHz
		{1601, 801}, // odd
		{0, 0},      // empty
		{1, 1},      // single sample
	}
	for _, tc := range cases {
		input := make([]int16, tc.inputLen)
		got := kaiserFIRDownsample2x(input)
		if len(got) != tc.wantLen {
			t.Errorf("kaiserFIRDownsample2x(len=%d): got len %d, want %d",
				tc.inputLen, len(got), tc.wantLen)
		}
	}
}

// TestKaiserFIRDownsample2xPassband verifies that a 1kHz sine at 16kHz input
// passes through the downsampler with less than 1 dB attenuation.
// 1kHz is well within the 4kHz passband of the anti-alias filter.
func TestKaiserFIRDownsample2xPassband(t *testing.T) {
	const (
		srcRate    = 16000
		dstRate    = 8000
		freq       = 1000.0 // Hz — well within 4kHz passband
		n          = 3200   // 200ms at 16kHz — long enough for stable measurement
		amplitude  = 16000.0
		maxAttenDB = 1.0 // allow at most 1 dB passband loss
	)

	// Generate 1kHz sine at 16kHz.
	input := make([]int16, n)
	for i := range input {
		input[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(srcRate)))
	}

	out := kaiserFIRDownsample2x(input)

	// Measure RMS of output vs expected amplitude at 1kHz @ 8kHz.
	// Skip first and last 16 samples (group delay of 31-tap filter = 15 samples,
	// but we use 16 as a conservative margin).
	skip := 16
	var inPow, outPow float64
	for i := skip; i < len(out)-skip; i++ {
		ideal := amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(dstRate))
		inPow += ideal * ideal
		outPow += float64(out[i]) * float64(out[i])
	}
	if inPow == 0 {
		t.Fatal("input power is zero")
	}
	// Gain = outRMS / inRMS; convert to dB.
	attenDB := 10 * math.Log10(outPow/inPow)
	t.Logf("1kHz passband: output/input power ratio = %.3f dB (want > -%.1f dB)", attenDB, maxAttenDB)
	if attenDB < -maxAttenDB {
		t.Errorf("1kHz passband attenuation %.2f dB exceeds %.1f dB limit", -attenDB, maxAttenDB)
	}
}

// TestKaiserFIRDownsample2xStopband verifies that a 5kHz sine at 16kHz input
// (above the 4kHz Nyquist of the 8kHz output) is attenuated by at least 30 dB.
func TestKaiserFIRDownsample2xStopband(t *testing.T) {
	const (
		srcRate    = 16000
		freq       = 5000.0 // Hz — above 4kHz Nyquist; would alias without filter
		n          = 3200   // 200ms at 16kHz
		amplitude  = 16000.0
		minAttenDB = 30.0 // require at least 30 dB stopband rejection
	)

	input := make([]int16, n)
	for i := range input {
		input[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(srcRate)))
	}

	out := kaiserFIRDownsample2x(input)

	// Measure output RMS (any remaining energy is aliased or leaked).
	skip := 16
	var inPow, outPow float64
	for i := skip; i < n-skip; i++ {
		inPow += float64(input[i]) * float64(input[i])
	}
	for i := skip; i < len(out)-skip; i++ {
		outPow += float64(out[i]) * float64(out[i])
	}
	if inPow == 0 {
		t.Fatal("input power is zero")
	}
	// Compare per-sample power (outPow is over half as many samples).
	// Normalise by sample count before taking ratio so we compare power density.
	nIn := float64(n - 2*skip)
	nOut := float64(len(out) - 2*skip)
	if nOut <= 0 {
		t.Fatal("output too short to measure")
	}
	inPowPerSample := inPow / nIn
	outPowPerSample := outPow / nOut
	attenDB := 10 * math.Log10(outPowPerSample/inPowPerSample)
	t.Logf("5kHz stopband: output/input power density = %.2f dB (want <= -%.0f dB)", attenDB, minAttenDB)
	if attenDB > -minAttenDB {
		t.Errorf("5kHz stopband rejection only %.2f dB (need >= %.0f dB)", -attenDB, minAttenDB)
	}
}

// TestKaiserFIRDownsample2xRoundTrip verifies that upsampling 8→16kHz with
// kaiserFIRUpsample2x then downsampling 16→8kHz with kaiserFIRDownsample2x
// recovers a 1kHz sine within 3 dB of the original amplitude.
func TestKaiserFIRDownsample2xRoundTrip(t *testing.T) {
	const (
		srcRate   = 8000
		freq      = 1000.0 // Hz
		n         = 1600   // 200ms at 8kHz
		amplitude = 16000.0
		maxLossDB = 3.0 // allow at most 3 dB round-trip loss
	)

	// 1. Generate 1kHz sine at 8kHz.
	original := make([]int16, n)
	for i := range original {
		original[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(srcRate)))
	}

	// 2. Upsample 8→16kHz.
	up := kaiserFIRUpsample2x(original)

	// 3. Downsample 16→8kHz.
	recovered := kaiserFIRDownsample2x(up)

	if len(recovered) != n {
		t.Fatalf("round-trip length: got %d, want %d", len(recovered), n)
	}

	// 4. Compare power of recovered signal vs original over steady-state region.
	// Skip first and last 16 samples to avoid filter edge effects.
	skip := 16
	var origPow, recPow float64
	for i := skip; i < n-skip; i++ {
		origPow += float64(original[i]) * float64(original[i])
		recPow += float64(recovered[i]) * float64(recovered[i])
	}
	if origPow == 0 {
		t.Fatal("original power is zero")
	}
	lossDB := 10 * math.Log10(recPow/origPow)
	t.Logf("Round-trip 1kHz 8→16→8kHz: power ratio = %.3f dB (allow down to -%.1f dB)", lossDB, maxLossDB)
	if lossDB < -maxLossDB {
		t.Errorf("round-trip loss %.2f dB exceeds %.1f dB limit", -lossDB, maxLossDB)
	}
}

// TestResampleDownsample2xViaPublicAPI verifies that Resample(16000, 8000)
// routes through the FIR path by checking that output length is correct and
// that a 1kHz tone passes without aliasing artefacts (SNR vs ideal > 30 dB).
func TestResampleDownsample2xViaPublicAPI(t *testing.T) {
	const (
		srcRate   = 16000
		dstRate   = 8000
		freq      = 1000.0
		n         = 3200 // 200ms at 16kHz
		amplitude = 16000.0
		minSNR    = 30.0
	)

	input := make([]int16, n)
	for i := range input {
		input[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(srcRate)))
	}

	out, err := Resample(input, srcRate, dstRate)
	if err != nil {
		t.Fatalf("Resample(16000→8000): %v", err)
	}

	wantLen := n / 2
	if abs(len(out)-wantLen) > 1 {
		t.Errorf("output length: got %d, want %d (±1)", len(out), wantLen)
	}

	// SNR of output vs ideal 1kHz sine at 8kHz.
	skip := 16
	var sigPow, noisePow float64
	for i := skip; i < len(out)-skip; i++ {
		ideal := amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(dstRate))
		actual := float64(out[i])
		sigPow += ideal * ideal
		diff := ideal - actual
		noisePow += diff * diff
	}
	var snr float64
	if noisePow == 0 {
		snr = 100
	} else {
		snr = 10 * math.Log10(sigPow/noisePow)
	}
	t.Logf("Resample(16000→8000) 1kHz SNR = %.2f dB (min %.0f dB)", snr, minSNR)
	if snr < minSNR {
		t.Errorf("SNR %.2f dB below minimum %.0f dB", snr, minSNR)
	}
}
