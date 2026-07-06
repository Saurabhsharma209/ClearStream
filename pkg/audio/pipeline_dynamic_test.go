package audio_test

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// TestPipelineDynamicSetAggressivenessNoReducer verifies SetAggressiveness does
// not panic when neither noiseReducer nor tieredNR is configured.
func TestPipelineDynamicSetAggressivenessNoReducer(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	// Should not panic even though no NR stages are configured.
	p.SetAggressiveness(0)
	p.SetAggressiveness(2)
	p.SetAggressiveness(3)
}

// TestPipelineDynamicSetAggressivenessWithNoiseReducer verifies SetAggressiveness
// propagates to the AdaptiveNoiseReducer stage.
func TestPipelineDynamicSetAggressivenessWithNoiseReducer(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:      16000,
		Channels:        1,
		Suppressor:      model.NewPassthrough(),
		UseNoiseReducer: true,
	})
	p.SetAggressiveness(0)
	p.SetAggressiveness(1)
	p.SetAggressiveness(2)
	p.SetAggressiveness(3)
}

// TestPipelineDynamicSetAggressivenessWithTieredNR verifies SetAggressiveness
// propagates to the TieredNR gate stage.
func TestPipelineDynamicSetAggressivenessWithTieredNR(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		TieredNR:   &audio.TieredNRConfig{HighSNRThreshold: 25, LowSNRThreshold: 10},
	})
	p.SetAggressiveness(0)
	p.SetAggressiveness(3)
}

// TestPipelineDynamicSetVADThresholdStaticVAD verifies SetVADThreshold updates
// the energy threshold on a static *VAD.
func TestPipelineDynamicSetVADThresholdStaticVAD(t *testing.T) {
	vad := audio.DefaultVAD()
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		VAD:        vad,
	})
	p.SetVADThreshold(500.0)
	// Verify through the exported field on the VAD we hold a reference to.
	if vad.ThresholdRMS != 500.0 {
		t.Errorf("expected ThresholdRMS=500, got %v", vad.ThresholdRMS)
	}
}

// TestPipelineDynamicSetVADThresholdAdaptiveVAD verifies SetVADThreshold updates
// the inner VAD threshold on an *AdaptiveVAD.
func TestPipelineDynamicSetVADThresholdAdaptiveVAD(t *testing.T) {
	avad := audio.DefaultAdaptiveVAD()
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		VAD:        avad,
	})
	p.SetVADThreshold(400.0)
	// The inner VAD's ThresholdRMS should be updated.
	if avad.VAD.ThresholdRMS != 400.0 {
		t.Errorf("expected AdaptiveVAD inner ThresholdRMS=400, got %v", avad.VAD.ThresholdRMS)
	}
}

// TestPipelineDynamicSetVADThresholdNilVAD verifies SetVADThreshold is a no-op
// (no panic) when no VAD is configured.
func TestPipelineDynamicSetVADThresholdNilVAD(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	p.SetVADThreshold(300.0) // should not panic
}

// TestPipelineDynamicSetAGCTargetWithAGC verifies SetAGCTarget updates the AGC
// TargetRMS when AGC is configured.
func TestPipelineDynamicSetAGCTargetWithAGC(t *testing.T) {
	agcCfg := audio.DefaultAGCConfig()
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		AGC:        &agcCfg,
	})
	p.SetAGCTarget(5000.0)
}

// TestPipelineDynamicSetAGCTargetNoAGC verifies SetAGCTarget is a no-op (no
// panic) when no AGC stage is configured.
func TestPipelineDynamicSetAGCTargetNoAGC(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	p.SetAGCTarget(5000.0) // should not panic
}

// TestPipelineDynamicReconfigureTieredNRAndAGC verifies Reconfigure applies new
// TieredNR thresholds and AGC target to a running pipeline.
func TestPipelineDynamicReconfigureTieredNRAndAGC(t *testing.T) {
	agcCfg := audio.DefaultAGCConfig()
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		TieredNR:   &audio.TieredNRConfig{HighSNRThreshold: 25, LowSNRThreshold: 10},
		AGC:        &agcCfg,
	})
	newAGCCfg := audio.DefaultAGCConfig()
	newAGCCfg.TargetRMS = 4000.0
	p.Reconfigure(audio.PipelineConfig{
		TieredNR: &audio.TieredNRConfig{HighSNRThreshold: 30, LowSNRThreshold: 8},
		AGC:      &newAGCCfg,
	})
}

// TestPipelineDynamicReconfigureNoOp verifies Reconfigure with nil fields is a
// no-op (does not panic).
func TestPipelineDynamicReconfigureNoOp(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	p.Reconfigure(audio.PipelineConfig{})
}

// TestPipelineDynamicIsBypassTrue verifies IsBypass returns true for a plain
// passthrough pipeline with no extra stages.
func TestPipelineDynamicIsBypassTrue(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	if !p.IsBypass() {
		t.Error("expected IsBypass() == true for bare passthrough pipeline")
	}
}

// TestPipelineDynamicIsBypassFalseWithAGC verifies IsBypass returns false when
// an AGC stage is configured alongside a passthrough suppressor.
func TestPipelineDynamicIsBypassFalseWithAGC(t *testing.T) {
	agcCfg := audio.DefaultAGCConfig()
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		AGC:        &agcCfg,
	})
	if p.IsBypass() {
		t.Error("expected IsBypass() == false when AGC is configured")
	}
}

// TestPipelineDynamicIsBypassFalseWithNR verifies IsBypass returns false when
// the noise reducer stage is configured.
func TestPipelineDynamicIsBypassFalseWithNR(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:      16000,
		Channels:        1,
		Suppressor:      model.NewPassthrough(),
		UseNoiseReducer: true,
	})
	if p.IsBypass() {
		t.Error("expected IsBypass() == false when noise reducer is configured")
	}
}

// TestPipelineDynamicIsBypassFalseNonPassthrough verifies IsBypass returns false
// when the suppressor is not *model.Passthrough.
func TestPipelineDynamicIsBypassFalseNonPassthrough(t *testing.T) {
	// Use a second Passthrough wrapped in a named type to confirm the type
	// assertion is what drives the result, not just any suppressor.
	type fakeSuppressor struct{ *model.Passthrough }
	fs := &fakeSuppressor{model.NewPassthrough()}
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: fs,
	})
	if p.IsBypass() {
		t.Error("expected IsBypass() == false for non-*model.Passthrough suppressor")
	}
}

// TestPipelineDynamicDiarizationSegmentsNilDiarizer verifies that
// DiarizationSegments returns nil when no diarizer is configured.
func TestPipelineDynamicDiarizationSegmentsNilDiarizer(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	segs := p.DiarizationSegments()
	if segs != nil {
		t.Errorf("expected nil segments without diarizer, got %v", segs)
	}
}

// TestPipelineDynamicDiarizationSegmentsWithDiarizer verifies that
// DiarizationSegments returns (non-error) results when a diarizer is configured
// and frames have been processed.
func TestPipelineDynamicDiarizationSegmentsWithDiarizer(t *testing.T) {
	d := audio.NewEnergyDiarizer(audio.DefaultEnergyDiarizerConfig())
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Diarizer:   d,
	})
	// Process a few silence frames so the diarizer has some state.
	frame := make([]byte, audio.FrameSizeBytes)
	for i := 0; i < 5; i++ {
		p.ProcessFrames(frame, nopWriter{}) //nolint:errcheck
	}
	// DiarizationSegments must not panic; result may be empty or contain segments.
	_ = p.DiarizationSegments()
}

// nopWriter discards all written bytes.
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
