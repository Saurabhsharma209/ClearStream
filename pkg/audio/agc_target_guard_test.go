package audio

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

// TestPipelineSetAGCTargetRejectsNonPositive is a regression test:
// SetAGCTarget used to assign targetRMS directly onto AGCConfig with no
// validation, so a zero or negative value (e.g. an unchecked user/UI input)
// would silently drive AGC.Process's desired-gain calculation to zero or
// negative, muting or phase-inverting audio for the rest of the call with no
// error surfaced anywhere.
func TestPipelineSetAGCTargetRejectsNonPositive(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		AGC:        &AGCConfig{TargetRMS: 5000},
	})

	if p.agc.cfg.TargetRMS != 5000 {
		t.Fatalf("setup: expected initial TargetRMS 5000, got %v", p.agc.cfg.TargetRMS)
	}

	p.SetAGCTarget(0)
	if p.agc.cfg.TargetRMS != 5000 {
		t.Errorf("SetAGCTarget(0) must be ignored, got TargetRMS = %v, want unchanged 5000", p.agc.cfg.TargetRMS)
	}

	p.SetAGCTarget(-100)
	if p.agc.cfg.TargetRMS != 5000 {
		t.Errorf("SetAGCTarget(-100) must be ignored, got TargetRMS = %v, want unchanged 5000", p.agc.cfg.TargetRMS)
	}

	p.SetAGCTarget(6000)
	if p.agc.cfg.TargetRMS != 6000 {
		t.Errorf("SetAGCTarget(6000) should apply, got TargetRMS = %v, want 6000", p.agc.cfg.TargetRMS)
	}
}

// TestPipelineReconfigureRejectsNonPositiveAGCTarget covers the same guard
// via the Reconfigure() path, which used to write cfg.AGC.TargetRMS directly
// onto the running AGC, bypassing SetAGCTarget's validation entirely.
func TestPipelineReconfigureRejectsNonPositiveAGCTarget(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		AGC:        &AGCConfig{TargetRMS: 3000},
	})

	p.Reconfigure(PipelineConfig{AGC: &AGCConfig{TargetRMS: 0}})
	if p.agc.cfg.TargetRMS != 3000 {
		t.Errorf("Reconfigure with TargetRMS=0 must be ignored, got %v, want unchanged 3000", p.agc.cfg.TargetRMS)
	}

	p.Reconfigure(PipelineConfig{AGC: &AGCConfig{TargetRMS: 7000}})
	if p.agc.cfg.TargetRMS != 7000 {
		t.Errorf("Reconfigure with a valid TargetRMS should apply, got %v, want 7000", p.agc.cfg.TargetRMS)
	}
}
