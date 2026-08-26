package audio

import (
	"math"
	"testing"
)

func TestSNREstimatorPureSine(t *testing.T) {
	// Pure sine = high SNR (no noise component)
	signal := make([]int16, 160)
	for i := range signal {
		signal[i] = 8000
	}
	noise := make([]int16, 160)
	for i := range noise {
		noise[i] = 100 // small noise
	}
	e := SNREstimator{}
	snr := e.EstimateSNR(signal, noise)
	if snr < 30 {
		t.Errorf("expected SNR > 30dB for clean signal, got %.1f", snr)
	}
}

func TestSNREstimatorEmpty(t *testing.T) {
	e := SNREstimator{}
	snr := e.EstimateSNR(nil, nil)
	if snr != 0 {
		t.Errorf("expected 0 for empty input, got %.1f", snr)
	}
}

// TestSNREstimatorZeroNoisePower verifies the 60 dB ceiling when noise power is zero.
func TestSNREstimatorZeroNoisePower(t *testing.T) {
	e := SNREstimator{}
	signal := make([]int16, 160)
	for i := range signal {
		signal[i] = 10000
	}
	silence := make([]int16, 160)
	snr := e.EstimateSNR(signal, silence)
	if snr != 60 {
		t.Errorf("EstimateSNR with zero-noise: expected 60, got %.2f", snr)
	}
}

// TestSNREstimatorLowSNR verifies a weak signal against strong noise yields negative SNR.
func TestSNREstimatorLowSNR(t *testing.T) {
	e := SNREstimator{}
	signal := make([]int16, 160)
	noise := make([]int16, 160)
	for i := range signal {
		signal[i] = 100  // weak signal
		noise[i] = 30000 // strong noise
	}
	snr := e.EstimateSNR(signal, noise)
	if snr >= 0 {
		t.Errorf("expected negative SNR for weak signal/strong noise, got %.2f", snr)
	}
	t.Logf("Low SNR: %.2f dB", snr)
}

// TestSNRImprovementFinite verifies SNRImprovement returns a finite result.
func TestSNRImprovementFinite(t *testing.T) {
	e := SNREstimator{}
	noisy := make([]int16, 160)
	clean := make([]int16, 160)
	for i := range noisy {
		noisy[i] = int16(10000 + (i%10)*500)
		clean[i] = 10000
	}
	improvement := e.SNRImprovement(noisy, clean)
	if math.IsNaN(improvement) || math.IsInf(improvement, 0) {
		t.Errorf("SNRImprovement returned non-finite value: %v", improvement)
	}
	t.Logf("SNR improvement: %.2f dB", improvement)
}

// TestSNRImprovementEmptyInputs verifies no panic and zero return on empty inputs.
func TestSNRImprovementEmptyInputs(t *testing.T) {
	e := SNREstimator{}
	result := e.SNRImprovement([]int16{}, []int16{})
	if result != 0 {
		t.Errorf("SNRImprovement(empty, empty) = %.2f, want 0", result)
	}
}

// TestPowerFunction exercises the internal power helper via SNR calculations.
func TestPowerViaEstimateSNR(t *testing.T) {
	e := SNREstimator{}
	// power([1000]*160) = 1000*1000 = 1e6
	// power([100]*160)  = 100*100   = 1e4
	// SNR = 10*log10(1e6/1e4) = 10*log10(100) = 20 dB
	signal := make([]int16, 160)
	noise := make([]int16, 160)
	for i := range signal {
		signal[i] = 1000
		noise[i] = 100
	}
	snr := e.EstimateSNR(signal, noise)
	if math.Abs(snr-20.0) > 0.01 {
		t.Errorf("expected 20.0 dB, got %.4f", snr)
	}
}

func TestSNRImprovementZeroNoise(t *testing.T) {
	// When noisy == clean, diff is all zeros → EstimateSNR returns 60 dB
	est := &SNREstimator{}
	signal := []int16{1000, 2000, -1000, 500}
	improvement := est.SNRImprovement(signal, signal)
	// snrBefore = SNR(signal, zeros) = 60; snrAfter = SNR(signal, zeros) = 60; diff = 0
	_ = improvement // just ensure no panic
}

func TestSNRImprovementDifferentLengths(t *testing.T) {
	est := &SNREstimator{}
	noisy := []int16{1000, 2000, 3000, 4000, 5000}
	clean := []int16{900, 1900, 2900} // shorter
	result := est.SNRImprovement(noisy, clean)
	_ = result // just ensure no panic; uses minLen
}

func TestSNRImprovementEmpty(t *testing.T) {
	est := &SNREstimator{}
	result := est.SNRImprovement(nil, []int16{1, 2})
	if result != 0 {
		t.Errorf("SNRImprovement(nil, ...) = %f, want 0", result)
	}
	result2 := est.SNRImprovement([]int16{1, 2}, nil)
	if result2 != 0 {
		t.Errorf("SNRImprovement(..., nil) = %f, want 0", result2)
	}
}

func TestSNRImprovementClip(t *testing.T) {
	// Force int32 overflow path in SNRImprovement
	est := &SNREstimator{}
	noisy := []int16{32767}
	clean := []int16{-32768}
	// diff = 32767 - (-32768) = 65535 > 32767 → clips to 32767
	result := est.SNRImprovement(noisy, clean)
	_ = result // just exercise the clip path
}
