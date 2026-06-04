package audio

import (
	"testing"
)

func TestTieredNR_HighSNR(t *testing.T) {
	nr := NewTieredNR(DefaultTieredNRConfig())

	// Warm up noise floor with quiet frames so the EMA converges to a low value.
	quiet := make([]int16, FrameSizeSamples)
	for i := 0; i < 50; i++ {
		nr.Process(quiet) //nolint:errcheck
	}

	// High-amplitude sine — after a quiet warmup its SNR will be well above
	// HighSNRThreshold (25 dB), exercising the gate-only path.
	frame := makeSineFrame(8000, 3) // amp=8000, ~3 cycles per frame
	out, err := nr.Process(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(frame) {
		t.Fatalf("output length mismatch: got %d want %d", len(out), len(frame))
	}
}

func TestTieredNR_LowSNR_Fallback(t *testing.T) {
	cfg := DefaultTieredNRConfig()
	cfg.LowSNRThreshold = 100 // force low-SNR path for every frame
	// nil deepfilter → gate fallback
	nr := NewTieredNR(cfg)
	frame := make([]int16, FrameSizeSamples)
	for i := range frame {
		frame[i] = int16(i % 100)
	}
	out, err := nr.Process(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != FrameSizeSamples {
		t.Fatalf("unexpected output length %d", len(out))
	}
}

func TestTieredNR_MidSNR_RNNoiseNilFallback(t *testing.T) {
	// With both thresholds set so the mid tier is always selected,
	// and RNNoise = nil, we expect the gate to handle it without error.
	cfg := TieredNRConfig{
		HighSNRThreshold: 1000, // never high-SNR
		LowSNRThreshold:  -1,   // never low-SNR
		RNNoise:          nil,
		DeepFilter:       nil,
	}
	nr := NewTieredNR(cfg)
	frame := makeSineFrame(1000, 2)
	out, err := nr.Process(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(frame) {
		t.Fatalf("output length mismatch: got %d want %d", len(out), len(frame))
	}
}

func TestTieredNR_Reset(t *testing.T) {
	nr := NewTieredNR(DefaultTieredNRConfig())
	frame := make([]int16, FrameSizeSamples)
	nr.Process(frame) //nolint:errcheck
	nr.Reset()        // must not panic
	if nr.noiseFloor != 1.0 {
		t.Fatalf("noiseFloor not reset: got %f", nr.noiseFloor)
	}
}

func TestTieredNR_EstimateSNR_QuietFrame(t *testing.T) {
	nr := NewTieredNR(DefaultTieredNRConfig())
	// A silent frame should yield SNR ≈ 0 dB (rms=1, noiseFloor=1 → 20*log10(1)=0)
	frame := make([]int16, FrameSizeSamples)
	nr.mu.Lock()
	snr := nr.estimateSNR(frame)
	nr.mu.Unlock()
	if snr < -1 || snr > 1 {
		t.Fatalf("expected SNR near 0 dB for silent frame, got %.2f", snr)
	}
}

func TestTieredNR_EmptyFrame(t *testing.T) {
	nr := NewTieredNR(DefaultTieredNRConfig())
	out, err := nr.Process([]int16{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output for empty input, got %d samples", len(out))
	}
}
