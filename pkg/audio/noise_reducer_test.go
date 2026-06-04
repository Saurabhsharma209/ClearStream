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
	// Use a simple deterministic pseudo-noise sequence (xorshift32) so tests
	// are reproducible without importing math/rand.
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

// TestAdaptiveNoiseReducer_ReducesNoise verifies that running a low-amplitude
// noise-dominated frame through the reducer produces lower RMS than the input.
func TestAdaptiveNoiseReducer_ReducesNoise(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up the noise floor estimate with several noise-only frames.
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 20; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// Now run a fresh noise frame and check reduction.
	inRMS := rmsInt16(noiseFrame)
	out, err := nr.Process(noiseFrame)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if len(out) != FrameSizeSamples {
		t.Fatalf("expected %d samples, got %d", FrameSizeSamples, len(out))
	}
	outRMS := rmsInt16(out)

	// After warmup the soft gate and Wiener gain should reduce RMS by at least 20%.
	threshold := inRMS * 0.80
	if outRMS >= threshold {
		t.Errorf("expected outRMS < %.2f (80%% of inRMS %.2f), got %.2f", threshold, inRMS, outRMS)
	}
}

// TestAdaptiveNoiseReducer_PreservesSpeech verifies that a high-amplitude sine
// (simulated speech) is not over-suppressed: output RMS must be within 20% of
// input RMS.
func TestAdaptiveNoiseReducer_PreservesSpeech(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up with noise so that the noise floor is established.
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 20; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// High-amplitude sine (well above SpeechThresh=200) — simulates speech.
	speechFrame := makeSineFrame(8000, 3)
	inRMS := rmsInt16(speechFrame)

	out, err := nr.Process(speechFrame)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	outRMS := rmsInt16(out)

	// Output should be at least 80% of input (not over-suppressed).
	minAllowed := inRMS * 0.80
	if outRMS < minAllowed {
		t.Errorf("speech over-suppressed: outRMS %.2f < %.2f (80%% of inRMS %.2f)", outRMS, minAllowed, inRMS)
	}
}

// TestAdaptiveNoiseReducer_Reset verifies that Reset clears internal state so
// that the reducer behaves identically to a freshly constructed one.
func TestAdaptiveNoiseReducer_Reset(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up with low-amplitude noise frames (below SpeechThresh) so the
	// noise floor EMA accumulates non-zero values in bandNoiseFloor.
	noiseFrame := addNoise(make([]int16, FrameSizeSamples), 150)
	for i := 0; i < 30; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// Confirm state is non-zero.
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

	// All internal fields must be zeroed.
	for i, v := range nr.bandNoiseFloor {
		if v != 0 {
			t.Errorf("bandNoiseFloor[%d] = %v after Reset, want 0", i, v)
		}
	}
	for i, v := range nr.bandSignalLevel {
		if v != 0 {
			t.Errorf("bandSignalLevel[%d] = %v after Reset, want 0", i, v)
		}
	}
	if nr.globalNoiseEMA != 0 {
		t.Errorf("globalNoiseEMA = %v after Reset, want 0", nr.globalNoiseEMA)
	}
	if nr.frameCount != 0 {
		t.Errorf("frameCount = %d after Reset, want 0", nr.frameCount)
	}
	if nr.peakHold != 0 {
		t.Errorf("peakHold = %v after Reset, want 0", nr.peakHold)
	}
}
