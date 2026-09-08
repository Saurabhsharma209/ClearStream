package audio

import (
	"math"
	"testing"
)

// TestNewAGCRejectsNonPositiveTimeConstants is a regression test: NewAGC used
// to default AttackMs/ReleaseMs (and TargetRMS/MaxGain) only when they were
// exactly 0, so any negative value (an unchecked config load, a UI slider
// that allows negative input, a decrement-past-zero bug in a caller) passed
// straight through into attackSamples/releaseSamples. That flips the sign of
// the exponential time constant, producing an attack/release coefficient
// >= 1 instead of the intended (0,1) range -- Process's per-sample smoothing
// (currentGain = coef*currentGain + (1-coef)*targetGain) then diverges
// geometrically every sample instead of converging: an unbounded gain
// runaway rather than a graceful fallback to the documented default.
func TestNewAGCRejectsNonPositiveTimeConstants(t *testing.T) {
	cfg := AGCConfig{
		TargetRMS:          -100,
		MaxGain:            -5,
		AttackMs:           -20,
		ReleaseMs:          -200,
		SoftLimitThreshold: 28000,
		SampleRate:         16000,
	}
	agc := NewAGC(cfg)

	if agc.cfg.TargetRMS != 3000 {
		t.Errorf("negative TargetRMS: got %v, want default 3000", agc.cfg.TargetRMS)
	}
	if agc.cfg.MaxGain != 4.0 {
		t.Errorf("negative MaxGain: got %v, want default 4.0", agc.cfg.MaxGain)
	}
	if agc.cfg.AttackMs != 20 {
		t.Errorf("negative AttackMs: got %v, want default 20", agc.cfg.AttackMs)
	}
	if agc.cfg.ReleaseMs != 200 {
		t.Errorf("negative ReleaseMs: got %v, want default 200", agc.cfg.ReleaseMs)
	}
	if agc.attackCoef <= 0 || agc.attackCoef >= 1 {
		t.Errorf("attackCoef = %v, want in (0,1)", agc.attackCoef)
	}
	if agc.releaseCoef <= 0 || agc.releaseCoef >= 1 {
		t.Errorf("releaseCoef = %v, want in (0,1)", agc.releaseCoef)
	}

	// Drive many frames of a moderate, steady-level signal through Process
	// and confirm the gain converges/stays bounded rather than diverging.
	// Before the fix, a coef >= 1 here would blow currentGain up without
	// bound within a handful of frames.
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = 500
	}
	for i := 0; i < 200; i++ {
		agc.Process(frame)
		g := agc.CurrentGain()
		if math.IsNaN(g) || math.IsInf(g, 0) {
			t.Fatalf("frame %d: currentGain diverged to %v", i, g)
		}
		if g > agc.cfg.MaxGain*1.01 {
			t.Fatalf("frame %d: currentGain %v exceeded MaxGain %v", i, g, agc.cfg.MaxGain)
		}
	}
}
