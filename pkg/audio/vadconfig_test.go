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

// TestVADConfigDefaults verifies that zero-value VADConfig fields get sensible
// defaults: EnergyThreshold=300.0 and HangoverFrames=8.
func TestVADConfigDefaults(t *testing.T) {
	sup := &noopSuppressor{}
	cfg := PipelineConfig{
		SampleRate: 16000,
		Suppressor: sup,
		// Intentionally zero-value: both EnergyThreshold and HangoverFrames are 0.
		VADConfig: &VADConfig{},
	}
	p := NewPipeline(cfg)
	if p.vad == nil {
		t.Fatal("expected NewPipeline to create a VAD from zero-value VADConfig, got nil")
	}
	staticVAD, ok := p.vad.(*VAD)
	if !ok {
		t.Fatalf("expected p.vad to be *VAD, got %T", p.vad)
	}

	// Verify EnergyThreshold default = 300.
	if staticVAD.ThresholdRMS != 300.0 {
		t.Errorf("EnergyThreshold default: want 300.0, got %.2f", staticVAD.ThresholdRMS)
	}

	// Verify HangoverFrames default = 8.
	if staticVAD.HangoverFrames != 8 {
		t.Errorf("HangoverFrames default: want 8, got %d", staticVAD.HangoverFrames)
	}

	// Borderline speech test: a frame whose RMS equals threshold (300) should be
	// classified as speech (>= threshold, not strictly >).
	borderlineFrame := make([]int16, FrameSizeSamples)
	// RMS of a constant value v over N samples = v. Set all samples to 300.
	for i := range borderlineFrame {
		borderlineFrame[i] = 300
	}
	if !staticVAD.IsSpeech(borderlineFrame) {
		t.Error("expected IsSpeech=true for frame with RMS=300 (equal to threshold=300)")
	}

	// Hangover test: confirm default of 8 silent frames after speech are still
	// treated as speech (prevents word-end clipping).
	staticVAD.Reset()
	staticVAD.IsSpeech(borderlineFrame) // prime the hangover counter

	silenceFrame := make([]int16, FrameSizeSamples) // all zeros, RMS=0
	hangoverCount := 0
	for i := 0; i < 20; i++ {
		if staticVAD.IsSpeech(silenceFrame) {
			hangoverCount++
		} else {
			break
		}
	}
	if hangoverCount != 8 {
		t.Errorf("HangoverFrames default=8: expected 8 hangover frames, got %d", hangoverCount)
	}

	// The frame immediately after hangover expiry must be silence.
	if staticVAD.IsSpeech(silenceFrame) {
		t.Error("frame after hangover expiry (8 frames) should be classified as silence")
	}
}
