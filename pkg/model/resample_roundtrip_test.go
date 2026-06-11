//go:build rnnoise

package model

import (
	"math"
	"testing"
)

// TestUpsampleDownsampleRoundtrip verifies that upsample3x→downsample3x
// preserves a 100Hz sine wave with < 1% distortion (max error < 300/32767).
func TestUpsampleDownsampleRoundtrip(t *testing.T) {
	const (
		sampleRate = 16000
		freq       = 100.0 // Hz — well below the 8kHz Nyquist
		nSamples   = 160   // one 10ms frame at 16kHz
		amplitude  = 10000 // int16 amplitude
	)

	// Generate input sine wave.
	input := make([]int16, nSamples)
	for i := range input {
		input[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/sampleRate))
	}

	// Roundtrip: upsample to 48kHz, downsample back to 16kHz.
	upsampled := upsample3x(input)
	if len(upsampled) != nSamples*3 {
		t.Fatalf("upsample3x: want %d samples, got %d", nSamples*3, len(upsampled))
	}
	downsampled := downsample3x(upsampled)
	if len(downsampled) != nSamples {
		t.Fatalf("downsample3x: want %d samples, got %d", nSamples, len(downsampled))
	}

	// Check max absolute error (skip first 2 samples — FIR group delay).
	var maxErr int16
	for i := 2; i < nSamples; i++ {
		diff := input[i] - downsampled[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxErr {
			maxErr = diff
		}
	}
	t.Logf("upsample3x→downsample3x roundtrip: max error = %d / %d (%.2f%%)",
		maxErr, amplitude, float64(maxErr)/float64(amplitude)*100)
	if maxErr > 300 {
		t.Errorf("roundtrip max error %d exceeds tolerance 300 (> 1%% of amplitude %d)",
			maxErr, amplitude)
	}
}

// TestUpsampleHighFreqRoundtrip verifies that upsample3x->downsample3x preserves
// a 3000Hz sine wave with < 6% distortion (Catmull-Rom quality gate).
func TestUpsampleHighFreqRoundtrip(t *testing.T) {
	const (
		sampleRate = 16000
		freq       = 3000.0
		nSamples   = 160
		amplitude  = 10000
		tolerance  = 600
	)
	input := make([]int16, nSamples)
	for i := range input {
		input[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/sampleRate))
	}
	upsampled := upsample3x(input)
	if len(upsampled) != nSamples*3 {
		t.Fatalf("upsample3x: want %d samples, got %d", nSamples*3, len(upsampled))
	}
	downsampled := downsample3x(upsampled)
	if len(downsampled) != nSamples {
		t.Fatalf("downsample3x: want %d samples, got %d", nSamples, len(downsampled))
	}
	var maxErr int16
	for i := 2; i < nSamples; i++ {
		diff := input[i] - downsampled[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxErr {
			maxErr = diff
		}
	}
	t.Logf("3kHz roundtrip: max error = %d / %d (%.2f%%)", maxErr, amplitude, float64(maxErr)/float64(amplitude)*100)
	if maxErr > tolerance {
		t.Errorf("3kHz roundtrip max error %d exceeds tolerance %d", maxErr, tolerance)
	}
}

// TestUpsampleMonotonicity verifies upsample3x does not overshoot input amplitude by >10%.
func TestUpsampleMonotonicity(t *testing.T) {
	const (
		sampleRate = 16000
		freq       = 1000.0
		nSamples   = 160
		amplitude  = 10000
	)
	maxAllowed := int16(int(amplitude) * 11 / 10)
	input := make([]int16, nSamples)
	for i := range input {
		input[i] = int16(amplitude * math.Sin(2*math.Pi*freq*float64(i)/sampleRate))
	}
	upsampled := upsample3x(input)
	var maxAbs int16
	for _, s := range upsampled {
		v := s
		if v < 0 {
			v = -v
		}
		if v > maxAbs {
			maxAbs = v
		}
	}
	t.Logf("upsample3x monotonicity: max abs = %d (allowed %d)", maxAbs, maxAllowed)
	if maxAbs > maxAllowed {
		t.Errorf("upsample3x overshoot: max abs %d exceeds allowed %d", maxAbs, maxAllowed)
	}
}

