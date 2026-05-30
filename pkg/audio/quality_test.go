package audio

import "testing"

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
