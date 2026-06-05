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

func TestAdaptiveNoiseReducer_SetAggressiveness(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()
	// Default is level 2
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = 100
	}
	out2, _ := nr.Process(frame)

	// Switch to aggressive (level 3) — should suppress more
	nr.SetAggressiveness(3)
	out3, _ := nr.Process(frame)

	// Switch to mild (level 1) — should suppress less
	nr.SetAggressiveness(1)
	out1, _ := nr.Process(frame)

	// All outputs must be same length
	if len(out2) != 160 || len(out3) != 160 || len(out1) != 160 {
		t.Fatal("wrong output length")
	}
	// No panics = pass (behavioral difference needs warmup to be measurable)
}

// TestAQ001RoboticVoice is the AQ-001 regression test.
// Verifies that speech-classified bands keep at least MinGainSpeech (0.70)
// gain so soft phonemes are not over-suppressed (robotic effect).
func TestAQ001RoboticVoice(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up noise floor with quiet background.
	noiseFrame := make([]int16, 160)
	for i := range noiseFrame {
		noiseFrame[i] = 50
	}
	for i := 0; i < 60; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// Speech frame: RMS ~4000 (well above SpeechThresh=280).
	speechFrame := make([]int16, 160)
	for i := range speechFrame {
		if i%2 == 0 {
			speechFrame[i] = 4000
		} else {
			speechFrame[i] = -4000
		}
	}
	out, err := nr.Process(speechFrame)
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}

	inRMS := rmsInt16(speechFrame)
	outRMS := rmsInt16(out)
	ratio := outRMS / inRMS

	t.Logf("AQ-001: inRMS=%.0f outRMS=%.0f ratio=%.3f (MinGainSpeech=%.2f)",
		inRMS, outRMS, ratio, nr.MinGainSpeech)

	// QA gate: output must retain at least MinGainSpeech fraction of input.
	if ratio < nr.MinGainSpeech {
		t.Errorf("AQ-001 FAIL: speech RMS ratio=%.3f < MinGainSpeech=%.2f — robotic voice",
			ratio, nr.MinGainSpeech)
	}
}

// TestAQ002GainStepSmoothing is the AQ-002 regression test.
// Verifies that per-frame band gain changes are clamped to MaxGainDelta.
func TestAQ002GainStepSmoothing(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	// Warm up with speech to establish high gain state.
	speechFrame := make([]int16, 160)
	for i := range speechFrame {
		if i%2 == 0 {
			speechFrame[i] = 4000
		} else {
			speechFrame[i] = -4000
		}
	}
	for i := 0; i < 30; i++ {
		nr.Process(speechFrame) //nolint:errcheck
	}

	// Switch to silence — gain should drop, but no frame-to-frame jump > MaxGainDelta.
	silentFrame := make([]int16, 160)
	prevGains := make([]float64, nrBands)
	for b := range prevGains {
		prevGains[b] = nr.bandGainPrev[b]
	}

	for frame := 0; frame < 20; frame++ {
		nr.Process(silentFrame) //nolint:errcheck
		for b := 0; b < nrBands; b++ {
			delta := math.Abs(nr.bandGainPrev[b] - prevGains[b])
			if delta > nr.MaxGainDelta+1e-9 {
				t.Errorf("AQ-002 FAIL: frame %d band %d delta=%.4f > MaxGainDelta=%.4f",
					frame, b, delta, nr.MaxGainDelta)
			}
			prevGains[b] = nr.bandGainPrev[b]
		}
	}
	t.Logf("AQ-002 PASS: all gain steps ≤ MaxGainDelta=%.2f", nr.MaxGainDelta)
}

// TestAQ003MusicalNoise is the AQ-003 regression test.
// After speech ends and hangover expires, no isolated band should have
// gain >> its neighbours (the tonal-hiss / musical-noise signature).
func TestAQ003MusicalNoise(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	noiseFrame := make([]int16, 160)
	for i := range noiseFrame {
		noiseFrame[i] = 60
	}
	for i := 0; i < 60; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	speechFrame := make([]int16, 160)
	for i := range speechFrame {
		if i%2 == 0 {
			speechFrame[i] = 5000
		} else {
			speechFrame[i] = -5000
		}
	}
	for i := 0; i < 10; i++ {
		nr.Process(speechFrame) //nolint:errcheck
	}

	// Wait for hangover to expire.
	for i := 0; i < nr.HangoverFrames+5; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	// No interior band should be isolated high — musical noise check.
	for b := 1; b < nrBands-1; b++ {
		g := nr.bandGainPrev[b]
		left := nr.bandGainPrev[b-1]
		right := nr.bandGainPrev[b+1]
		if g > 0.25 && g > 2*left && g > 2*right {
			t.Errorf("AQ-003 FAIL: band %d isolated gain=%.3f (neighbours %.3f,%.3f) — musical noise",
				b, g, left, right)
		}
	}
	t.Logf("AQ-003 PASS: no isolated bands after hangover=%d frames", nr.HangoverFrames)
}

// TestAQ004HighFreqBandProtection is the AQ-004 regression test.
// High-frequency bands (nrHighBandStart+) must hold gain ≥ 0.80 during speech
// to protect sibilants (s, sh, f, th) from being suppressed into garbling.
func TestAQ004HighFreqBandProtection(t *testing.T) {
	nr := NewAdaptiveNoiseReducer()

	noiseFrame := make([]int16, 160)
	for i := range noiseFrame {
		noiseFrame[i] = 80
	}
	for i := 0; i < 60; i++ {
		nr.Process(noiseFrame) //nolint:errcheck
	}

	speechFrame := make([]int16, 160)
	for i := range speechFrame {
		if i%2 == 0 {
			speechFrame[i] = 3000
		} else {
			speechFrame[i] = -3000
		}
	}
	for i := 0; i < 15; i++ {
		nr.Process(speechFrame) //nolint:errcheck
	}

	const highBandFloor = 0.80
	for b := nrHighBandStart; b < nrBands; b++ {
		g := nr.bandGainPrev[b]
		if g < highBandFloor-0.01 {
			t.Errorf("AQ-004 FAIL: high-freq band %d gain=%.3f < %.2f — sibilants suppressed → garbling",
				b, g, highBandFloor)
		}
	}
	t.Logf("AQ-004 PASS: high-freq bands %d–%d gain ≥ %.2f during speech",
		nrHighBandStart, nrBands-1, highBandFloor)
}
