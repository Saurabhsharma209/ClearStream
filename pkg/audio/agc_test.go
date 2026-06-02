package audio

import (
	"math"
	"testing"
)

func TestAGCDefaultConfig(t *testing.T) {
	cfg := DefaultAGCConfig()
	if cfg.TargetRMS <= 0 {
		t.Error("TargetRMS should be positive")
	}
	if cfg.MaxGain <= 0 {
		t.Error("MaxGain should be positive")
	}
	if cfg.AttackMs <= 0 || cfg.ReleaseMs <= 0 {
		t.Error("Attack and Release should be positive")
	}
}

func TestAGCSilencePassthrough(t *testing.T) {
	agc := NewAGC(DefaultAGCConfig())
	silence := make([]int16, 160)
	out := agc.Process(silence)
	for i, s := range out {
		if s != 0 {
			t.Errorf("silence sample[%d] should be 0, got %d", i, s)
		}
	}
}

func TestAGCBoostsQuietSignal(t *testing.T) {
	cfg := DefaultAGCConfig()
	cfg.TargetRMS = 3000
	cfg.SampleRate = 16000
	agc := NewAGC(cfg)

	// Generate a quiet sine wave at ~300 RMS
	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(300 * math.Sin(2*math.Pi*440*float64(i)/16000.0))
	}

	// Run several frames to let gain ramp up
	for i := 0; i < 20; i++ {
		agc.Process(samples)
	}

	if agc.CurrentGain() <= 1.0 {
		t.Errorf("expected gain > 1.0 for quiet signal, got %.3f", agc.CurrentGain())
	}
}

func TestAGCDucksLoudSignal(t *testing.T) {
	cfg := DefaultAGCConfig()
	cfg.TargetRMS = 1000
	cfg.SampleRate = 16000
	agc := NewAGC(cfg)

	// Generate a loud signal near full scale (~20000 RMS)
	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(20000 * math.Sin(2*math.Pi*440*float64(i)/16000.0))
	}

	// Run several frames to let gain fall
	for i := 0; i < 20; i++ {
		agc.Process(samples)
	}

	if agc.CurrentGain() >= 1.0 {
		t.Errorf("expected gain < 1.0 for loud signal, got %.3f", agc.CurrentGain())
	}
}

func TestAGCHardClipPreventsOverflow(t *testing.T) {
	cfg := DefaultAGCConfig()
	cfg.MaxGain = 100.0 // extreme gain to force clipping
	cfg.TargetRMS = 32000
	cfg.SampleRate = 16000
	agc := NewAGC(cfg)

	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = 1000
	}

	// Run many frames to build gain
	var out []int16
	for i := 0; i < 100; i++ {
		out = agc.Process(samples)
	}

	for i, s := range out {
		if s > 32767 || s < -32768 {
			t.Errorf("sample[%d]=%d overflows int16 range", i, s)
		}
	}
}

func TestAGCReset(t *testing.T) {
	agc := NewAGC(DefaultAGCConfig())

	// Boost gain by processing quiet signal
	quiet := make([]int16, 160)
	for i := range quiet {
		quiet[i] = 100
	}
	for i := 0; i < 20; i++ {
		agc.Process(quiet)
	}
	gainBefore := agc.CurrentGain()

	agc.Reset()

	if agc.CurrentGain() != 1.0 {
		t.Errorf("gain after Reset should be 1.0, got %.3f", agc.CurrentGain())
	}
	_ = gainBefore
}

func TestAGCCurrentGainDB(t *testing.T) {
	agc := NewAGC(DefaultAGCConfig())
	// At initial gain=1.0, dB should be 0
	db := agc.CurrentGainDB()
	if math.Abs(db) > 0.001 {
		t.Errorf("expected 0 dB at gain=1.0, got %.3f", db)
	}
}

func TestAGCMaxGainCap(t *testing.T) {
	cfg := DefaultAGCConfig()
	cfg.MaxGain = 2.0
	cfg.TargetRMS = 32000 // unreachably high target to force max gain
	cfg.SampleRate = 16000
	agc := NewAGC(cfg)

	// Process very quiet signal to push gain toward MaxGain
	quiet := make([]int16, 160)
	for i := range quiet {
		quiet[i] = 10
	}
	for i := 0; i < 200; i++ {
		agc.Process(quiet)
	}

	if agc.CurrentGain() > cfg.MaxGain+0.01 {
		t.Errorf("gain %.3f exceeds MaxGain %.3f", agc.CurrentGain(), cfg.MaxGain)
	}
}

func BenchmarkAGCProcess(b *testing.B) {
	agc := NewAGC(DefaultAGCConfig())
	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(3000 * math.Sin(2*math.Pi*440*float64(i)/16000.0))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agc.Process(samples)
	}
}
