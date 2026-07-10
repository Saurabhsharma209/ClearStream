package rtp

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
)

// This file closes the remaining pkg/rtp coverage gaps flagged in the
// project DEVLOG: putJitterPayload (75%), detectPitch (78.6%), and
// InjectBotAudio (80%). Each test below is annotated with the specific
// branch it exercises, cross-referenced against `go tool cover -func`
// output for jitter.go and playback.go.

// --- putJitterPayload (jitter.go) ------------------------------------------

// TestPutJitterPayload_Nil exercises the nil-guard early-return in
// putJitterPayload. In normal JitterBuffer operation this function is only
// ever called with a live payload slice, so the defensive nil check was
// never actually hit by any existing test.
func TestPutJitterPayload_Nil(t *testing.T) {
	// Must be a safe no-op: no panic, and the pool must not be handed a nil
	// backing slice.
	putJitterPayload(nil)
}

// TestPutJitterPayload_NonNil exercises the normal pooling path alongside
// the nil case above, and verifies a payload obtained via getJitterPayload
// can be round-tripped through the pool without panicking.
func TestPutJitterPayload_NonNil(t *testing.T) {
	b := getJitterPayload(32)
	if len(b) != 32 {
		t.Fatalf("getJitterPayload(32): want len 32, got %d", len(b))
	}
	putJitterPayload(b)
}

// --- detectPitch (jitter.go) ------------------------------------------------

// makeSineFrame generates a synthetic sine-wave PCM16 frame for pitch
// detection tests, mirroring the pattern used by TestDetectPitch in
// jitter_test.go.
func makeSineFrame(freqHz, sampleRate float64, n int, amp float64) []int16 {
	frame := make([]int16, n)
	for i := 0; i < n; i++ {
		v := amp * sinApprox(2.0*3.14159265*freqHz*float64(i)/sampleRate)
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		frame[i] = int16(v)
	}
	return frame
}

// TestDetectPitch_ShortFrame covers the "n < 80" early return: frames too
// short to run autocorrelation on should just return their own length.
func TestDetectPitch_ShortFrame(t *testing.T) {
	frame := make([]int16, 40)
	for i := range frame {
		frame[i] = 1000 // non-zero, so this isn't also exercising the silence path
	}
	got := detectPitch(frame)
	if got != len(frame) {
		t.Errorf("detectPitch(len=40 frame): want %d, got %d", len(frame), got)
	}
}

// TestDetectPitch_Silence covers the "energy < 1.0" fallback: a silent (or
// near-silent) frame should skip the O(n^2) autocorrelation search entirely
// and return n/4.
func TestDetectPitch_Silence(t *testing.T) {
	frame := make([]int16, 160) // all-zero PCM => energy == 0 < 1.0
	got := detectPitch(frame)
	want := len(frame) / 4
	if got != want {
		t.Errorf("detectPitch(silence): want %d, got %d", want, got)
	}
}

// TestDetectPitch_MaxLagCap covers the "maxLag > 450" clamp. For frames
// longer than 900 samples, n/2 exceeds 450 and the search window must be
// capped so the O(n^2) correlation cost stays bounded.
func TestDetectPitch_MaxLagCap(t *testing.T) {
	saved := prevDetectedPitch
	defer func() { prevDetectedPitch = saved }()
	prevDetectedPitch = 0 // isolate from continuity guard state left by other tests

	const freq = 100.0
	const sampleRate = 16000.0
	frame := makeSineFrame(freq, sampleRate, 1000, 8000.0) // n=1000 -> n/2=500 > 450 cap
	got := detectPitch(frame)

	if got <= 0 || got > 450 {
		t.Fatalf("detectPitch: expected a lag in (0, 450] after clamping, got %d", got)
	}
	// Sanity: true period is 16000/100 = 160 samples; confirm the search
	// still finds something in the right neighbourhood despite the clamp.
	wantPeriod := sampleRate / freq
	if diff := float64(got) - wantPeriod; diff < -40 || diff > 40 {
		t.Errorf("detectPitch(100Hz@16kHz, n=1000): want period near %.0f, got %d", wantPeriod, got)
	}
}

// TestDetectPitch_ContinuityGuard covers both the "prevDetectedPitch > 0"
// body and, within it, the octave-jump rejection branch ("ratio > 1.5 ||
// ratio < 0.67"). The very first detectPitch call in a process never enters
// the guard (prevDetectedPitch starts at 0); this test drives it through
// all three states: no-previous, previous-agrees, previous-disagrees.

// --- InjectBotAudio (playback.go) -------------------------------------------

// TestInjectBotAudio_EmptyCodecDefaults covers the `codec == ""` half of
// InjectBotAudio's fallback-to-G711U condition. NewSession() normally
// resolves cfg.Codec to a concrete value before a Session is usable, so this
// branch is only reachable by forcing cfg.Codec back to its zero value
// afterward -- guarding against a future refactor bypassing that
// normalization.
func TestInjectBotAudio_EmptyCodecDefaults(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.cfg.Codec = ""
	ok := sess.InjectBotAudio(zeroPCM(160))
	if !ok {
		t.Error("InjectBotAudio: expected true for single frame with empty codec")
	}
	if stats := sess.PlaybackStats(); stats.Pushed != 1 {
		t.Errorf("Pushed: want 1, got %d", stats.Pushed)
	}
}

// TestInjectBotAudio_UnknownCodecDefaults covers the `codec ==
// audio.CodecUnknown` half of the same fallback condition.
func TestInjectBotAudio_UnknownCodecDefaults(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.cfg.Codec = audio.CodecUnknown
	ok := sess.InjectBotAudio(zeroPCM(160))
	if !ok {
		t.Error("InjectBotAudio: expected true for single frame with CodecUnknown")
	}
}

// TestInjectBotAudio_G711A covers the `case audio.CodecG711A` switch arm in
// both the full-frame loop and the padded final-frame handler -- neither was
// previously exercised since no other test configures an explicit A-law
// session.
func TestInjectBotAudio_G711A(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.cfg.Codec = audio.CodecG711A
	ok := sess.InjectBotAudio(zeroPCM(2*160 + 80)) // 2 full frames + 1 padded remainder
	if !ok {
		t.Error("InjectBotAudio: expected true for G711A multi-frame input")
	}
	if stats := sess.PlaybackStats(); stats.Pushed != 3 {
		t.Errorf("Pushed: want 3 (2 full + 1 padded), got %d", stats.Pushed)
	}
}

// TestInjectBotAudio_FullFramePushFailure covers the `allAccepted = false`
// assignment inside the full-frame loop: when the playback queue is at
// capacity, Push() fails for a full frame and InjectBotAudio must report
// false while continuing to process (and drop) the rest.
func TestInjectBotAudio_FullFramePushFailure(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.playback = NewPlaybackQueue(1) // room for exactly one frame

	ok := sess.InjectBotAudio(zeroPCM(2 * 160)) // 2 full frames, no remainder
	if ok {
		t.Error("InjectBotAudio: expected false when the queue can't hold all frames")
	}
	stats := sess.PlaybackStats()
	if stats.Pushed != 1 {
		t.Errorf("Pushed: want 1, got %d", stats.Pushed)
	}
	if stats.Dropped != 1 {
		t.Errorf("Dropped: want 1, got %d", stats.Dropped)
	}
}

// TestInjectBotAudio_RemainderPushFailure covers the `allAccepted = false`
// assignment inside the padded-remainder handler specifically (as opposed
// to the full-frame loop above). The queue is pre-filled to capacity so the
// only Push() attempt InjectBotAudio makes -- the padded final frame --
// fails.
func TestInjectBotAudio_RemainderPushFailure(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.playback = NewPlaybackQueue(1)
	if !sess.playback.Push(make([]byte, 10)) {
		t.Fatal("setup: expected to fill the queue to capacity")
	}

	ok := sess.InjectBotAudio(zeroPCM(80)) // remainder-only input, no full frames
	if ok {
		t.Error("InjectBotAudio: expected false when the remainder Push fails")
	}
	if stats := sess.PlaybackStats(); stats.Dropped != 1 {
		t.Errorf("Dropped: want 1, got %d", stats.Dropped)
	}
}
func TestDetectPitch_ContinuityGuard(t *testing.T) {
	saved := prevDetectedPitch
	defer func() { prevDetectedPitch = saved }()

	// Call 1: prevDetectedPitch == 0 -> guard body skipped (false path).
	prevDetectedPitch = 0
	frame440 := makeSineFrame(440.0, 16000.0, 160, 8000.0)
	first := detectPitch(frame440)
	if first < 30 || first > 45 {
		t.Fatalf("detectPitch(440Hz) call 1: expected ~36, got %d", first)
	}

	// Call 2: prevDetectedPitch was left at `first` by call 1. Same signal
	// again => ratio ~1.0, within [0.67, 1.5], so the guard body runs but
	// must NOT override the freshly computed lag.
	second := detectPitch(frame440)
	if second != first {
		t.Errorf("detectPitch(440Hz) call 2: guard should not fire when ratio~1.0, want %d, got %d", first, second)
	}

	// Call 3: force a stale, wildly different previous period so that the
	// newly computed lag for the same 440Hz frame deviates from it by more
	// than 50%. The guard must reject the new estimate and reuse the stale
	// previous period instead.
	prevDetectedPitch = 300
	third := detectPitch(frame440)
	if third != 300 {
		t.Errorf("detectPitch: octave-jump guard should reuse stale previous period 300, got %d", third)
	}
}
