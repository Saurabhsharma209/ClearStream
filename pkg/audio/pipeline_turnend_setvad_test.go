package audio_test

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// TestSetVADThresholdPropagatesToTurnEndTracker verifies that
// Pipeline.SetVADThreshold updates not only the suppression-bypass VAD but
// also TurnEnd's independent, hangover-free VAD clone (cloneVADForTurnEnd in
// turnend.go). That clone is built once at NewPipeline time from whatever
// threshold the configured VAD held then; without propagating later
// SetVADThreshold calls into it, TurnEnd() would silently keep classifying
// frames against the stale construction-time threshold for the rest of the
// call, permanently out of sync with the live suppression-bypass gate.
func TestSetVADThresholdPropagatesToTurnEndTracker(t *testing.T) {
	// Start with a deliberately insensitive VAD (threshold 10000): the
	// "speech" frame below (amplitude 2500) would be classified as silence
	// under this threshold.
	vad := &audio.VAD{ThresholdRMS: 10000, HangoverFrames: 0}
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		VAD:               vad,
		TurnEndBufferSize: 4,
	})

	// Lower the threshold well below the amplitude of the frame fed next.
	// This must reach TurnEnd's own VAD clone, not just p.vad.
	p.SetVADThreshold(500)

	// One frame of "speech" at amplitude 2500 (above the new 500 threshold,
	// below the stale 10000 one), then enough silence to cross TurnEnd's
	// ~200ms (20-frame) sustained-silence threshold.
	feedTurnEndFrames(t, p, 2500, 1)
	feedTurnEndFrames(t, p, 0, 25)

	select {
	case ev := <-p.TurnEnd():
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
	default:
		t.Fatal("expected a TurnEndEvent after SetVADThreshold lowered the threshold below the speech frame's amplitude, but none was fired -- SetVADThreshold did not propagate to TurnEnd's independent VAD clone")
	}
}

// TestSetVADThresholdNilTurnEndNoPanic verifies SetVADThreshold's new
// turnEnd-propagation call is a safe no-op when TurnEndBufferSize was never
// configured (turnEnd tracker is nil).
func TestSetVADThresholdNilTurnEndNoPanic(t *testing.T) {
	vad := &audio.VAD{ThresholdRMS: 300}
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		VAD:        vad,
	})
	p.SetVADThreshold(700) // must not panic
	if vad.ThresholdRMS != 700 {
		t.Fatalf("expected ThresholdRMS=700, got %v", vad.ThresholdRMS)
	}
}
