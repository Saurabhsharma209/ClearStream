package audio

import (
	"math"
	"testing"
)

// loudFrame48k returns a Frame48kSamples-length synthetic tone loud enough to
// drive the peak limiter, noise reducer, and tiered-NR internal state away
// from their zero/default values, so Reset() has something real to clear.
func loudFrame48k() []int16 {
	frame := make([]int16, Frame48kSamples)
	for i := range frame {
		frame[i] = int16(20000.0 * math.Sin(float64(i)*0.3))
	}
	return frame
}

// TestPipelineResetClearsNoiseReducerAndLimiter exercises Pipeline.Reset()'s
// noiseReducer.Reset(), limiter.Reset(), and the resample48k history-zeroing
// loops -- all four were reachable (UseNoiseReducer+UseLimiter+Process48k is
// a normal, documented configuration) but had zero test coverage, so a
// regression that dropped one of these Reset() calls (leaking noise-floor,
// peak-envelope, or resampler-history state across calls/streams) would have
// gone unnoticed.
func TestPipelineResetClearsNoiseReducerAndLimiter(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 48000,
		Suppressor:      &noopSuppressor{},
		UseNoiseReducer: true,
		UseLimiter:      true,
	})

	frame := loudFrame48k()
	for i := 0; i < 60; i++ {
		if _, err := p.Process48k(frame); err != nil {
			t.Fatalf("Process48k error: %v", err)
		}
	}

	// Sanity: processing real audio must have actually perturbed the state
	// this test is about to verify gets cleared -- otherwise the assertions
	// below would pass trivially even if Reset() were a no-op.
	if p.noiseReducer.globalNoiseEMA == 0 {
		t.Fatal("setup invariant broken: noiseReducer.globalNoiseEMA still 0 after 60 loud frames")
	}
	if p.limiter.peak == 0 {
		t.Fatal("setup invariant broken: limiter.peak still 0 after 60 loud frames")
	}
	histNonZero := false
	for _, s := range p.resample48kDownHist {
		if s != 0 {
			histNonZero = true
			break
		}
	}
	if !histNonZero {
		t.Fatal("setup invariant broken: resample48kDownHist still all-zero after 60 loud frames")
	}

	p.Reset()

	if p.noiseReducer.globalNoiseEMA != 0 {
		t.Errorf("Reset() did not clear noiseReducer.globalNoiseEMA, got %v", p.noiseReducer.globalNoiseEMA)
	}
	for b, v := range p.noiseReducer.bandNoiseFloor {
		if v != 0 {
			t.Errorf("Reset() did not clear noiseReducer.bandNoiseFloor[%d], got %v", b, v)
		}
	}
	if p.limiter.peak != 0 {
		t.Errorf("Reset() did not clear limiter.peak, got %v", p.limiter.peak)
	}
	for i, s := range p.resample48kDownHist {
		if s != 0 {
			t.Errorf("Reset() did not zero resample48kDownHist[%d], got %d", i, s)
		}
	}
	for i, s := range p.resample48kUpHist {
		if s != 0 {
			t.Errorf("Reset() did not zero resample48kUpHist[%d], got %d", i, s)
		}
	}
}

// TestPipelineResetClearsTieredNR mirrors
// TestPipelineResetClearsNoiseReducerAndLimiter for the TieredNR path
// (mutually exclusive with the flat AdaptiveNoiseReducer -- TieredNR takes
// priority when both are configured), which also had zero coverage on its
// Reset() call.
//
// This drives tieredNR.Process directly (rather than via Pipeline.Process48k)
// with a small constant-amplitude 16kHz frame so the noiseFloor EMA update
// -- gated on rms < noiseFloor*3, see estimateSNR -- is exercised precisely,
// without the 3x Kaiser-FIR downsample's rounding-to-zero behavior on
// very-low-amplitude 48kHz input swamping the signal before it ever reaches
// TieredNR.
func TestPipelineResetClearsTieredNR(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 48000,
		Suppressor:      &noopSuppressor{},
		TieredNR:        &TieredNRConfig{HighSNRThreshold: 25.0, LowSNRThreshold: 10.0},
	})

	if p.tieredNR == nil {
		t.Fatal("setup invariant broken: TieredNR config did not produce a non-nil tieredNR")
	}

	quiet := make([]int16, FrameSizeSamples)
	for i := range quiet {
		quiet[i] = 2
	}
	for i := 0; i < 60; i++ {
		if _, err := p.tieredNR.Process(quiet); err != nil {
			t.Fatalf("tieredNR.Process error: %v", err)
		}
	}

	if p.tieredNR.noiseFloor == 1.0 {
		t.Fatal("setup invariant broken: tieredNR.noiseFloor still at its initial 1.0 after 60 quiet frames")
	}

	p.Reset()

	if p.tieredNR.noiseFloor != 1.0 {
		t.Errorf("Reset() did not restore tieredNR.noiseFloor to 1.0, got %v", p.tieredNR.noiseFloor)
	}
}
