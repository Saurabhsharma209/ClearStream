package audio

import (
	"bytes"
	"math"
	"testing"
)

func TestAECBypass(t *testing.T) {
	aec := NewAEC(DefaultAECConfig())

	nearEnd := []int16{100, 200, -100, 0, 32767, -32768}

	// nil farEnd
	out := aec.Process(nil, nearEnd)
	for i, v := range out {
		if v != nearEnd[i] {
			t.Errorf("nil farEnd: sample %d: got %d, want %d", i, v, nearEnd[i])
		}
	}

	// empty farEnd
	out = aec.Process([]int16{}, nearEnd)
	for i, v := range out {
		if v != nearEnd[i] {
			t.Errorf("empty farEnd: sample %d: got %d, want %d", i, v, nearEnd[i])
		}
	}
}

func aecRMS(samples []int16) float64 {
	sum := 0.0
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func sineFrame(freq, sampleRate float64, offset, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		t := float64(offset+i) / sampleRate
		out[i] = int16(10000 * math.Sin(2*math.Pi*freq*t))
	}
	return out
}

func TestAECReducesEcho(t *testing.T) {
	const (
		filterLen  = 64
		sampleRate = 16000
		frameSize  = 160
		numFrames  = 300
		freq       = 440.0
	)

	cfg := AECConfig{
		FilterLen:  filterLen,
		StepSize:   0.1,
		Leakage:    0.9999,
		SampleRate: sampleRate,
	}
	aec := NewAEC(cfg)

	// Run many frames: near-end = far-end (perfect echo scenario)
	var lastInputRMS, lastOutputRMS float64
	for f := 0; f < numFrames; f++ {
		farEnd := sineFrame(freq, sampleRate, f*frameSize, frameSize)
		nearEnd := sineFrame(freq, sampleRate, f*frameSize, frameSize)
		out := aec.Process(farEnd, nearEnd)
		if f >= numFrames-10 {
			lastInputRMS += aecRMS(nearEnd)
			lastOutputRMS += aecRMS(out)
		}
	}
	lastInputRMS /= 10
	lastOutputRMS /= 10

	// After adaptation, output RMS should be significantly lower than input RMS
	if lastOutputRMS >= lastInputRMS*0.5 {
		t.Errorf("AEC did not converge: inputRMS=%.2f outputRMS=%.2f (expected output < 50%% of input)", lastInputRMS, lastOutputRMS)
	}
}

func TestAECReset(t *testing.T) {
	const (
		filterLen  = 64
		sampleRate = 16000
		frameSize  = 160
		numFrames  = 300
		freq       = 440.0
	)

	cfg := AECConfig{
		FilterLen:  filterLen,
		StepSize:   0.1,
		Leakage:    0.9999,
		SampleRate: sampleRate,
	}
	aec := NewAEC(cfg)

	// First convergence
	for f := 0; f < numFrames; f++ {
		farEnd := sineFrame(freq, sampleRate, f*frameSize, frameSize)
		nearEnd := sineFrame(freq, sampleRate, f*frameSize, frameSize)
		aec.Process(farEnd, nearEnd)
	}

	// Reset and check filter state is zeroed
	aec.Reset()
	for i, v := range aec.w {
		if v != 0 {
			t.Errorf("w[%d] = %f after Reset, want 0", i, v)
		}
	}
	for i, v := range aec.refBuf {
		if v != 0 {
			t.Errorf("refBuf[%d] = %f after Reset, want 0", i, v)
		}
	}
	if aec.pos != 0 {
		t.Errorf("pos = %d after Reset, want 0", aec.pos)
	}

	// After reset, filter should converge again
	var lastInputRMS, lastOutputRMS float64
	for f := 0; f < numFrames; f++ {
		farEnd := sineFrame(freq, sampleRate, f*frameSize, frameSize)
		nearEnd := sineFrame(freq, sampleRate, f*frameSize, frameSize)
		out := aec.Process(farEnd, nearEnd)
		if f >= numFrames-10 {
			lastInputRMS += aecRMS(nearEnd)
			lastOutputRMS += aecRMS(out)
		}
	}
	lastInputRMS /= 10
	lastOutputRMS /= 10

	if lastOutputRMS >= lastInputRMS*0.5 {
		t.Errorf("AEC did not re-converge after Reset: inputRMS=%.2f outputRMS=%.2f", lastInputRMS, lastOutputRMS)
	}
}

func TestNarrowbandAECConfig(t *testing.T) {
	cfg := NarrowbandAECConfig()
	if cfg.SampleRate != 8000 {
		t.Errorf("SampleRate = %d, want 8000", cfg.SampleRate)
	}
	if cfg.FilterLen != 256 {
		t.Errorf("FilterLen = %d, want 256", cfg.FilterLen)
	}
}

func TestDefaultAECConfig(t *testing.T) {
	cfg := DefaultAECConfig()
	if cfg.SampleRate != 16000 {
		t.Errorf("SampleRate = %d, want 16000", cfg.SampleRate)
	}
	if cfg.FilterLen != 512 {
		t.Errorf("FilterLen = %d, want 512", cfg.FilterLen)
	}
}

func TestAECPipelineWired(t *testing.T) {
	aecCfg := DefaultAECConfig()
	suppressor := newMockSuppressorGain1()
	cfg := PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      suppressor,
		AEC:             &aecCfg,
	}
	p := NewPipeline(cfg)
	if p.aec == nil {
		t.Fatal("pipeline.aec should not be nil when AEC config is set")
	}

	// Provide a far-end signal and process a frame
	farEnd := sineFrame(440, 16000, 0, 160)
	p.SetFarEnd(farEnd)

	frame := make([]byte, 320)
	var out bytes.Buffer
	if err := p.ProcessFrames(frame, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	if out.Len() != 320 {
		t.Errorf("output len = %d, want 320", out.Len())
	}
}
