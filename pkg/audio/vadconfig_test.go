package audio

import (
	"testing"
)

// TestVADConfigWiring verifies that PipelineConfig.VADConfig constructs a *VAD
// with the specified EnergyThreshold and HangoverFrames when PipelineConfig.VAD is nil.
func TestVADConfigWiring(t *testing.T) {
	sup := &noopSuppressor{}
	cfg := PipelineConfig{
		SampleRate: 16000,
		Suppressor: sup,
		VADConfig: &VADConfig{
			EnergyThreshold: 500,
			HangoverFrames:  3,
		},
	}
	p := NewPipeline(cfg)
	if p.vad == nil {
		t.Fatal("expected NewPipeline to create a VAD from VADConfig, got nil")
	}

	staticVAD, ok := p.vad.(*VAD)
	if !ok {
		t.Fatalf("expected p.vad to be *VAD, got %T", p.vad)
	}
	if staticVAD.ThresholdRMS != 500 {
		t.Errorf("ThresholdRMS: want 500, got %.2f", staticVAD.ThresholdRMS)
	}
	if staticVAD.HangoverFrames != 3 {
		t.Errorf("HangoverFrames: want 3, got %d", staticVAD.HangoverFrames)
	}

	// Verify threshold behaviour: frame with RMS=1000 (> 500) must be speech.
	speechFrame := make([]int16, FrameSizeSamples)
	for i := range speechFrame {
		speechFrame[i] = 1000
	}
	if !staticVAD.IsSpeech(speechFrame) {
		t.Error("expected IsSpeech=true for frame with RMS=1000 and threshold=500")
	}

	// Reset hangover so the next silent frames start fresh.
	staticVAD.Reset()

	// Frame with RMS=0 (all zeros) must be silence (below threshold=500).
	silenceFrame := make([]int16, FrameSizeSamples)
	if staticVAD.IsSpeech(silenceFrame) {
		t.Error("expected IsSpeech=false for zero frame with threshold=500")
	}

	// Verify HangoverFrames=3: feed one speech frame then count how many
	// subsequent silent frames are still returned as speech (should be exactly 3).
	staticVAD.Reset()
	staticVAD.IsSpeech(speechFrame) // prime hangover

	hangoverCount := 0
	for i := 0; i < 10; i++ {
		if staticVAD.IsSpeech(silenceFrame) {
			hangoverCount++
		} else {
			break
		}
	}
	if hangoverCount != 3 {
		t.Errorf("HangoverFrames=3: expected 3 hangover frames, got %d", hangoverCount)
	}

	// The next silent frame after hangover expires must be classified as silence.
	if staticVAD.IsSpeech(silenceFrame) {
		t.Error("frame after hangover expiry should be silence")
	}
}

// TestVADConfigDoesNotOverrideExplicitVAD verifies that a pre-set PipelineConfig.VAD
// takes precedence over a non-nil VADConfig (VADConfig is ignored when VAD is set).
func TestVADConfigDoesNotOverrideExplicitVAD(t *testing.T) {
	sup := &noopSuppressor{}
	explicit := &VAD{ThresholdRMS: 999, HangoverFrames: 7}
	cfg := PipelineConfig{
		SampleRate: 16000,
		Suppressor: sup,
		VAD:        explicit,
		VADConfig: &VADConfig{
			EnergyThreshold: 500,
			HangoverFrames:  3,
		},
	}
	p := NewPipeline(cfg)
	if p.vad != explicit {
		t.Errorf("expected explicit VAD to be used; got %+v", p.vad)
	}
}
