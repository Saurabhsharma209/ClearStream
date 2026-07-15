package audio_test

import (
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// These tests close a real coverage gap in cloneVADForTurnEnd (pkg/audio/turnend.go):
// the *VAD and *AdaptiveVAD branches were previously exercised by zero tests --
// every existing TurnEnd test left PipelineConfig.VAD/VADConfig/UseAdaptiveVAD
// unset, so cloneVADForTurnEnd only ever hit its "vad == nil" default branch
// (13.3% coverage). Since a bypass-VAD + TurnEnd combination is exactly what
// LangStream's ASR-trigger integration is expected to configure in production
// (VAD bypass for CPU savings, TurnEnd for utterance-boundary detection), the
// untested cloning logic that keeps the two independent deserved direct coverage.

// TestTurnEndWithStaticVADUsesConfiguredThreshold verifies that when a *VAD is
// configured for suppression-bypass gating (PipelineConfig.VAD), TurnEnd's
// internal clone honors that VAD's actual ThresholdRMS rather than silently
// falling back to the generic default (300) that cloneVADForTurnEnd otherwise
// uses when no VAD is configured at all.
func TestTurnEndWithStaticVADUsesConfiguredThreshold(t *testing.T) {
	// A custom, high threshold (5000) that would classify amplitude-1000
	// frames as silence, whereas the generic fallback default (300) would
	// classify them as speech. If cloneVADForTurnEnd incorrectly ignored the
	// configured *VAD's threshold, this test's "no event" assertion below
	// would fail.
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		VAD:               &audio.VAD{ThresholdRMS: 5000, HangoverFrames: 0},
		TurnEndBufferSize: 4,
	})
	ch := p.TurnEnd()
	if ch == nil {
		t.Fatal("expected non-nil TurnEnd() channel")
	}

	// Amplitude 1000 is above the generic fallback default (300) but below
	// the configured threshold (5000) -- these frames must be classified as
	// silence by TurnEnd's clone, so sawSpeech should never become true and
	// no event should ever fire despite plenty of trailing "silence".
	feedTurnEndFrames(t, p, 1000, 10)
	feedTurnEndFrames(t, p, 0, 40)

	select {
	case ev := <-ch:
		t.Fatalf("expected no TurnEndEvent: amplitude-1000 frames should be classified as silence under the configured 5000 threshold, got %+v", ev)
	default:
	}

	// Sanity check: amplitude comfortably above the configured threshold
	// must still be treated as speech and produce a TurnEndEvent once
	// followed by sustained silence, proving the "no event" result above
	// isn't due to some unrelated breakage.
	feedTurnEndFrames(t, p, 8000, 10)
	feedTurnEndFrames(t, p, 0, 25)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TurnEndEvent for amplitude-8000 speech above the configured threshold, got none")
	}
}

// TestTurnEndWithStaticVADIgnoresBypassHangover verifies that TurnEnd's clone
// forces HangoverFrames to 0 regardless of what the bypass-gating *VAD is
// configured with. If cloneVADForTurnEnd ever regressed to copy
// HangoverFrames verbatim, a large bypass hangover would make the clone
// misclassify early silent frames as speech (via its own hangover countdown),
// delaying -- or in the worst case suppressing -- the turn-end firing that
// should occur at exactly the 20-frame (200ms) silence threshold.
func TestTurnEndWithStaticVADIgnoresBypassHangover(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		// A large bypass hangover (20 frames = 200ms): if this leaked into
		// TurnEnd's clone, the first 20 silent frames after speech would be
		// consumed by the clone's own hangover countdown instead of being
		// counted as real silence, and the assertion below would fail.
		VAD:               &audio.VAD{ThresholdRMS: 300, HangoverFrames: 20},
		TurnEndBufferSize: 4,
	})
	ch := p.TurnEnd()

	feedTurnEndFrames(t, p, 5000, 10)
	// Exactly the 20-frame (200ms) silence threshold -- no more, no less.
	feedTurnEndFrames(t, p, 0, 20)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TurnEndEvent at exactly the 200ms silence threshold; a bypass VAD hangover leak into the TurnEnd clone would delay this past 20 frames")
	}
}

// TestTurnEndWithVADConfigPropagatesThreshold verifies the PipelineConfig.VADConfig
// construction path (as opposed to directly-constructed *VAD via
// PipelineConfig.VAD) also correctly propagates its threshold into TurnEnd's
// clone. VADConfig is the "named, discoverable" configuration path documented
// on PipelineConfig.VADConfig, and is a plausible integration entry point for
// LangStream, so it gets its own direct coverage rather than relying solely
// on the PipelineConfig.VAD-based tests above.
func TestTurnEndWithVADConfigPropagatesThreshold(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		VADConfig:         &audio.VADConfig{EnergyThreshold: 4000},
		TurnEndBufferSize: 4,
	})
	ch := p.TurnEnd()

	// Below the configured 4000 threshold -- must not register as speech.
	feedTurnEndFrames(t, p, 1000, 10)
	feedTurnEndFrames(t, p, 0, 40)

	select {
	case ev := <-ch:
		t.Fatalf("expected no TurnEndEvent under the configured VADConfig threshold, got %+v", ev)
	default:
	}
}

// TestTurnEndWithAdaptiveVADFiresAfterCalibration verifies that TurnEnd works
// correctly end-to-end when PipelineConfig.UseAdaptiveVAD is set -- exercising
// cloneVADForTurnEnd's *AdaptiveVAD branch, which (like the *VAD branch above)
// previously had no coverage at all. TurnEnd's clone must independently
// calibrate its own noise floor (it does not share calibration state with the
// bypass-gating AdaptiveVAD instance) and, once calibrated, must still detect
// a real speech-then-silence utterance boundary.
func TestTurnEndWithAdaptiveVADFiresAfterCalibration(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		UseAdaptiveVAD:    true,
		TurnEndBufferSize: 4,
	})
	ch := p.TurnEnd()
	if ch == nil {
		t.Fatal("expected non-nil TurnEnd() channel")
	}

	// DefaultAdaptiveVAD calibrates over 50 frames; feed low-amplitude
	// "noise" during calibration. Must not panic and must not fire (the
	// AdaptiveVAD treats all frames as silence while calibrating).
	feedTurnEndFrames(t, p, 50, 50)

	select {
	case ev := <-ch:
		t.Fatalf("expected no TurnEndEvent during AdaptiveVAD calibration, got %+v", ev)
	default:
	}

	// Post-calibration: clear speech well above the calibrated noise floor,
	// then sustained silence at roughly the calibration noise level.
	feedTurnEndFrames(t, p, 6000, 10)
	feedTurnEndFrames(t, p, 50, 30)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TurnEndEvent after post-calibration speech+silence, got none")
	}
}

// TestTurnEndWithAdaptiveVADIgnoresBypassHangover mirrors
// TestTurnEndWithStaticVADIgnoresBypassHangover for the *AdaptiveVAD clone
// path: DefaultAdaptiveVAD configures an 8-frame (80ms) HangoverFrames on its
// embedded VAD, and cloneVADForTurnEnd must zero it on the clone so the
// 200ms silence threshold is measured directly rather than inflated by
// leaked hangover.
func TestTurnEndWithAdaptiveVADIgnoresBypassHangover(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		UseAdaptiveVAD:    true,
		TurnEndBufferSize: 4,
	})
	ch := p.TurnEnd()

	// Calibrate on low-amplitude noise.
	feedTurnEndFrames(t, p, 50, 50)
	// Speech burst, then exactly the 20-frame (200ms) silence threshold.
	feedTurnEndFrames(t, p, 6000, 10)
	feedTurnEndFrames(t, p, 50, 20)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TurnEndEvent at exactly the 200ms silence threshold; DefaultAdaptiveVAD's 8-frame hangover leaking into the TurnEnd clone would delay this")
	}
}
