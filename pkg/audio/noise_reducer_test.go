package audio

import (
	"math"
	"testing"
)

// rmsInt16 computes the root-mean-square of a slice of int16 samples.
func rmsInt16(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// makeSineFrame generates a 160-sample sine wave at the given amplitude and
// frequency (in cycles per frame, so freq=1 → one full cycle per frame).
func makeSineFrame(amp float64, freq float64) []int16 {
	out := make([]int16, FrameSizeSamples)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(FrameSizeSamples)))
	}
	return out
}

// addNoise adds white noise with the given amplitude to samples (in-place copy).
func addNoise(samples []int16, noiseAmp float64) []int16 {
	out := make([]int16, len(samples))
	// Deterministic xorshift32 so tests are reproducible without math/rand.
	var state uint32 = 0xdeadbeef
	for i, s := range samples {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		noise := (float64(int32(state)>>16) / 32768.0) * noiseAmp
		v := float64(s) + noise
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}

// TestAdaptiveNoiseReducer_ReducesNoise verifies that a noise-dominated frame
// is reduced in RMS after the noise floor EMA has been primed.
func TestAdaptiveNoiseReducer_ReducesNoise(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up the noise floor estimate with noise-only frames (RMS ≈ 75, well
	// below SpeechThresh=280 so the noise EMA converges quickly).
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 60; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	inRMS := rmsInt16(noiseFrame)
	out, err := nr.Process(noiseFrame)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if len(out) != FrameSizeSamples {
		t.Fatalf("expected %d samples, got %d", FrameSizeSamples, len(out))
	}
	outRMS := rmsInt16(out)

	// After the global noise EMA stabilises the soft gate fires: output RMS
	// should be reduced to at most 80% of input RMS.
	threshold := inRMS * 0.80
	if outRMS >= threshold {
		t.Errorf("expected outRMS < %.2f (80%% of inRMS %.2f), got %.2f",
			threshold, inRMS, outRMS)
	}
}

// TestAdaptiveNoiseReducer_PreservesSpeech verifies that high-amplitude speech
// is not over-suppressed: output RMS must be at least 75% of input RMS.
// (The minimum gain floor MinGainSpeech=0.55 guarantees at least 55% with fully
// converged noise floor; with warm gain state from prevGain=1.0 the actual
// preservation is typically >90%.)
func TestAdaptiveNoiseReducer_PreservesSpeech(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Establish noise floor.
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 60; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// High-amplitude sine (RMS ≈ 5657, far above SpeechThresh=280).
	speechFrame := makeSineFrame(8000, 3)
	inRMS := rmsInt16(speechFrame)

	out, err := nr.Process(speechFrame)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	outRMS := rmsInt16(out)

	// With temporal smoothing prevGain≈1.0 → smoothed gain ≈1.0 on first speech
	// frame. Require at least 75% preservation.
	minAllowed := inRMS * 0.75
	if outRMS < minAllowed {
		t.Errorf("speech over-suppressed: outRMS %.2f < %.2f (75%% of inRMS %.2f)",
			outRMS, minAllowed, inRMS)
	}
}

// TestAdaptiveNoiseReducer_GainSmoothing verifies that the temporal gain
// smoothing is active: the variance of per-frame gain applied to identical
// noise frames should be very low (CoV < 0.3 after warmup).
func TestAdaptiveNoiseReducer_GainSmoothing(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Establish noise floor with stationary noise.
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 60; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// Collect output RMS across 20 identical frames.
	var gains []float64
	inRMS := rmsInt16(noiseFrame)
	for i := 0; i < 20; i++ {
		out, _ := nr.Process(noiseFrame)
		outRMS := rmsInt16(out)
		if inRMS > 0 {
			gains = append(gains, outRMS/inRMS)
		}
	}

	// Compute CoV.
	var mean float64
	for _, g := range gains {
		mean += g
	}
	mean /= float64(len(gains))
	var variance float64
	for _, g := range gains {
		d := g - mean
		variance += d * d
	}
	variance /= float64(len(gains))
	cov := math.Sqrt(variance) / (mean + 1e-9)

	// CoV should be < 0.3 (smooth). Old unsmoothed algo was ~0.72.
	if cov >= 0.30 {
		t.Errorf("gain CoV %.3f >= 0.30 — temporal smoothing may not be working", cov)
	}
}

// TestAdaptiveNoiseReducer_Reset verifies that Reset clears internal state.
func TestAdaptiveNoiseReducer_Reset(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up so internal state accumulates non-trivial values.
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 40; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// Confirm noise floor state is non-zero.
	hasState := false
	for _, v := range nr.bandNoiseFloor {
		if v != 0 {
			hasState = true
			break
		}
	}
	if !hasState {
		t.Fatal("expected non-zero bandNoiseFloor after warmup")
	}

	nr.Reset()

	for i, v := range nr.bandNoiseFloor {
		if v != 0 {
			t.Errorf("bandNoiseFloor[%d] = %v after Reset, want 0", i, v)
		}
	}
	// After Reset, prevGain should be 1.0 (pass-through, not 0).
	for i, v := range nr.bandGainPrev {
		if v != 1.0 {
			t.Errorf("bandGainPrev[%d] = %v after Reset, want 1.0", i, v)
		}
	}
	if nr.globalNoiseEMA != 0 {
		t.Errorf("globalNoiseEMA = %v after Reset, want 0", nr.globalNoiseEMA)
	}
	if nr.frameCount != 0 {
		t.Errorf("frameCount = %d after Reset, want 0", nr.frameCount)
	}
	if nr.hangoverCount != 0 {
		t.Errorf("hangoverCount = %d after Reset, want 0", nr.hangoverCount)
	}
}
