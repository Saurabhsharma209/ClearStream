package audio

import "testing"

// TestPipelineCurrentSpeaker exercises Pipeline.CurrentSpeaker (0% covered
// as of the 2026-08-26 daily build), the live per-frame counterpart to
// DiarizationSegments added alongside it on 08-17 for pkg/rtp.Session to
// surface "who is talking right now" in QualityReport.
func TestPipelineCurrentSpeaker(t *testing.T) {
	t.Run("no diarizer configured returns SpeakerUnknown", func(t *testing.T) {
		p := NewPipeline(PipelineConfig{SampleRate: 16000})
		if got := p.CurrentSpeaker(); got != SpeakerUnknown {
			t.Fatalf("CurrentSpeaker() = %q, want %q", got, SpeakerUnknown)
		}
	})

	t.Run("reflects diarizer's open segment as frames arrive", func(t *testing.T) {
		d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
		p := NewPipeline(PipelineConfig{SampleRate: 16000, Diarizer: d})

		// Before any frame is fed to the diarizer, the open segment defaults
		// to silence (see NewEnergyDiarizer).
		if got := p.CurrentSpeaker(); got != SpeakerSilence {
			t.Fatalf("CurrentSpeaker() before any frame = %q, want %q", got, SpeakerSilence)
		}

		// Feed the diarizer speech frames directly (CurrentSpeaker only reads
		// diarizer state; it does not require routing through
		// Pipeline.ProcessFrames, which additionally needs a configured
		// Suppressor).
		ts := int64(0)
		for i := 0; i < 10; i++ {
			d.ProcessFrame(makeSpeech(160), ts)
			ts += 10
		}
		if got := p.CurrentSpeaker(); got != SpeakerNearEnd {
			t.Fatalf("CurrentSpeaker() during active speech = %q, want %q", got, SpeakerNearEnd)
		}

		// DiarizationSegments only reports completed segments -- the open
		// one CurrentSpeaker just reported must not appear there yet, which
		// is the entire reason CurrentSpeaker exists per its doc comment.
		for _, seg := range p.DiarizationSegments() {
			if seg.Speaker == SpeakerNearEnd && seg.EndMs == -1 {
				t.Fatalf("open segment leaked into DiarizationSegments(): %+v", seg)
			}
		}
	})
}
