package audio_test

import (
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// makeTurnEndFrame builds one FrameSizeBytes (320-byte / 160-sample) PCM
// frame of constant-amplitude int16 samples. A constant-amplitude frame has
// RMS energy exactly equal to the amplitude, which keeps the speech/silence
// math in these tests easy to reason about: amplitude 5000 is well above the
// default TurnEnd energy threshold (300), and amplitude 0 is silence.
func makeTurnEndFrame(amplitude int16) []byte {
	samples := make([]int16, audio.FrameSizeSamples)
	for i := range samples {
		samples[i] = amplitude
	}
	b := make([]byte, audio.FrameSizeBytes)
	for i, v := range samples {
		b[2*i] = byte(v)
		b[2*i+1] = byte(v >> 8)
	}
	return b
}

// feedTurnEndFrames writes n frames of the given amplitude through the
// pipeline via ProcessFrames, discarding the cleaned output.
func feedTurnEndFrames(t *testing.T, p *audio.Pipeline, amplitude int16, n int) {
	t.Helper()
	frame := makeTurnEndFrame(amplitude)
	for i := 0; i < n; i++ {
		if err := p.ProcessFrames(frame, nopTurnEndWriter{}); err != nil {
			t.Fatalf("ProcessFrames failed: %v", err)
		}
	}
}

type nopTurnEndWriter struct{}

func (nopTurnEndWriter) Write(p []byte) (int, error) { return len(p), nil }

// newTurnEndPipeline builds a pipeline configured at 16kHz (no resampling)
// with a passthrough suppressor, so each ProcessFrames call consumes exactly
// one FrameSizeBytes (10ms) frame per iteration -- keeping the frame-count
// math in these tests exact.
func newTurnEndPipeline(turnEndBufferSize int) *audio.Pipeline {
	return audio.NewPipeline(audio.PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		TurnEndBufferSize: turnEndBufferSize,
	})
}

// TestTurnEndDisabledByDefault verifies that TurnEnd() returns nil when
// TurnEndBufferSize is left at its zero value -- the pipeline must not
// allocate a channel or do any turn-end bookkeeping.
func TestTurnEndDisabledByDefault(t *testing.T) {
	p := newTurnEndPipeline(0)
	if ch := p.TurnEnd(); ch != nil {
		t.Fatalf("expected nil TurnEnd() channel when TurnEndBufferSize is 0, got %v", ch)
	}

	// Feed a full speech-then-silence sequence; since the feature is
	// disabled this must not panic and must still return nil.
	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 30)
	if ch := p.TurnEnd(); ch != nil {
		t.Fatalf("expected nil TurnEnd() channel after processing frames, got %v", ch)
	}

	// Reset/Close must be no-ops (not panic) when the feature was never enabled.
	p.Reset()
	p.Close()
	p.Close() // double-close must also be safe
}

// TestTurnEndFiresOnceAfterSustainedSilence verifies that a synthetic
// speech-then-200ms-silence PCM stream triggers exactly one TurnEndEvent,
// even though silence continues well past the 200ms threshold.
func TestTurnEndFiresOnceAfterSustainedSilence(t *testing.T) {
	p := newTurnEndPipeline(4)
	ch := p.TurnEnd()
	if ch == nil {
		t.Fatal("expected non-nil TurnEnd() channel when TurnEndBufferSize > 0")
	}

	// 100ms of speech (10 frames @ 10ms) followed by 300ms of silence (30
	// frames): well past the 200ms (20-frame) threshold.
	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 30)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
		if ev.Timestamp.IsZero() {
			t.Error("expected non-zero Timestamp on TurnEndEvent")
		}
	case <-time.After(time.Second):
		t.Fatal("expected a TurnEndEvent, got none")
	}

	// Exactly one event should have fired despite 30 silent frames (10 more
	// than the 20-frame threshold): the tracker must not re-fire every frame
	// while silence continues.
	select {
	case ev := <-ch:
		t.Fatalf("expected exactly one TurnEndEvent, got a second: %+v", ev)
	default:
	}
}

// TestTurnEndNoFalseTriggerOnBriefPause verifies that a pause shorter than
// the 200ms silence threshold does not fire a TurnEndEvent.
func TestTurnEndNoFalseTriggerOnBriefPause(t *testing.T) {
	p := newTurnEndPipeline(4)
	ch := p.TurnEnd()

	// 100ms speech, then only 100ms silence (10 frames -- half the 200ms/20-frame
	// threshold), then speech resumes. This must never fire.
	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 10)
	feedTurnEndFrames(t, p, 5000, 10)
	// Now a second brief pause, again under threshold.
	feedTurnEndFrames(t, p, 0, 15)

	select {
	case ev := <-ch:
		t.Fatalf("expected no TurnEndEvent for a brief pause under 200ms, got %+v", ev)
	default:
	}
}

// TestTurnEndNoTriggerOnLeadingSilence verifies that silence occurring
// before any speech has been observed never fires a TurnEndEvent -- only
// silence *following* speech constitutes an utterance boundary.
func TestTurnEndNoTriggerOnLeadingSilence(t *testing.T) {
	p := newTurnEndPipeline(4)
	ch := p.TurnEnd()

	feedTurnEndFrames(t, p, 0, 40) // well over the 20-frame threshold, but no speech yet

	select {
	case ev := <-ch:
		t.Fatalf("expected no TurnEndEvent for leading silence with no prior speech, got %+v", ev)
	default:
	}
}

// TestTurnEndResetAllowsRefire verifies that Pipeline.Reset() clears the
// turn-end detector's per-utterance state (without closing the channel), so
// a second speech-then-silence utterance after Reset() still fires its own
// event on the same channel.
func TestTurnEndResetAllowsRefire(t *testing.T) {
	p := newTurnEndPipeline(4)
	ch := p.TurnEnd()

	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 25)

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected first TurnEndEvent before Reset")
	}

	// Reset (idempotent -- call twice) and run a second utterance.
	p.Reset()
	p.Reset()

	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 25)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200 on post-Reset event, got %d", ev.SilenceMs)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a second TurnEndEvent after Reset, got none")
	}

	// Channel must still be open (Reset must not close it).
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("TurnEnd() channel was closed by Reset(), expected it to remain open")
		}
	default:
	}
}

// TestTurnEndCloseClosesChannelIdempotently verifies that Pipeline.Close()
// closes the TurnEnd() channel, is safe to call multiple times, and does not
// panic even with buffered/no pending events.
func TestTurnEndCloseClosesChannelIdempotently(t *testing.T) {
	p := newTurnEndPipeline(4)
	ch := p.TurnEnd()
	if ch == nil {
		t.Fatal("expected non-nil TurnEnd() channel")
	}

	p.Close()
	p.Close() // must not panic on double-close

	// Draining a closed, empty channel must return the zero value with ok == false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after Close(), but a value was delivered with ok=true and no data was ever sent")
		}
	case <-time.After(time.Second):
		t.Fatal("expected closed channel read to return immediately")
	}
}

// TestTurnEndDropOldestOnFull verifies the non-blocking, drop-oldest-on-full
// delivery policy: with a buffer size of 1, two separate utterances (each
// producing a TurnEndEvent) must not block ProcessFrames, and the channel
// should end up holding the most recent event only.
func TestTurnEndDropOldestOnFull(t *testing.T) {
	p := newTurnEndPipeline(1)
	ch := p.TurnEnd()

	// First utterance -- fills the buffer (size 1).
	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 25)

	// Second utterance without draining the channel -- must not block.
	p.Reset()
	feedTurnEndFrames(t, p, 5000, 10)
	feedTurnEndFrames(t, p, 0, 25)

	select {
	case ev := <-ch:
		if ev.SilenceMs < 200 {
			t.Errorf("expected SilenceMs >= 200, got %d", ev.SilenceMs)
		}
	default:
		t.Fatal("expected a buffered TurnEndEvent to be available")
	}
	// Only one event should remain buffered (capacity 1); the first was
	// dropped in favor of the second per the drop-oldest-on-full policy.
	select {
	case ev := <-ch:
		t.Fatalf("expected buffer to hold only one event, got an extra: %+v", ev)
	default:
	}
}
