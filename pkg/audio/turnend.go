package audio

import (
	"sync"
	"time"
)

// turnEndSilenceFrameThreshold is the number of consecutive 10ms silent
// frames that must follow at least one speech frame before Pipeline.TurnEnd()
// fires an event. 20 frames * 10ms/frame = 200ms, matching the "energy drop +
// 200ms silence" end-of-utterance heuristic described in ROADMAP.md Phase 1
// ("VAD — energy-based turn marker").
const turnEndSilenceFrameThreshold = 20

// turnEndFrameDurationMs is the duration, in milliseconds, of a single
// processed frame. Both ProcessFrames (per loop iteration, after resampling
// to ProcessorSampleRate) and Process48k (per call) operate on exactly one
// 10ms frame, so a plain frame counter is a valid proxy for wall-clock
// silence duration regardless of the configured input sample rate.
const turnEndFrameDurationMs = 10

// TurnEndEvent is emitted on the channel returned by Pipeline.TurnEnd() when
// the pipeline detects end-of-utterance: at least one frame of speech energy
// followed by turnEndSilenceFrameThreshold (~200ms) of sustained silence.
type TurnEndEvent struct {
	// Timestamp is the wall-clock time the silence threshold was reached.
	Timestamp time.Time
	// SilenceMs is the sustained silence duration, in milliseconds, that
	// triggered this event (always >= ~200ms).
	SilenceMs int
}

// turnEndTracker holds the mutable per-pipeline state used to detect
// end-of-utterance from a stream of per-frame speech/silence classifications.
//
// It is intentionally kept separate from the VADer instance used for
// suppression-bypass gating (Pipeline.vad): that VAD's HangoverFrames window
// exists to avoid clipping trailing word energy during suppression, which is
// a different concern from "how long has it actually been silent" -- coupling
// the two would make the 200ms threshold's real-world meaning depend on
// whatever HangoverFrames happens to be configured. TurnEnd reuses the same
// energy-detection *algorithm* (via cloneVADForTurnEnd) but on an independent,
// hangover-free instance so the 200ms is measured directly.
type turnEndTracker struct {
	ch        chan TurnEndEvent
	closeOnce sync.Once

	vad VADer // dedicated, hangover-free energy detector; never nil when tracker is non-nil

	sawSpeech     bool
	silenceFrames int
}

// newTurnEndTracker builds a turnEndTracker for the given buffer size. If
// bufSize <= 0, it returns nil -- the zero-cost disabled state mirroring
// rtp.Session's CleanAudioBufferSize == 0 behaviour: no channel is allocated
// and callers pay no bookkeeping cost per frame beyond a nil check.
func newTurnEndTracker(bufSize int, vad VADer) *turnEndTracker {
	if bufSize <= 0 {
		return nil
	}
	return &turnEndTracker{
		ch:  make(chan TurnEndEvent, bufSize),
		vad: cloneVADForTurnEnd(vad),
	}
}

// cloneVADForTurnEnd returns an independent, hangover-free VADer instance
// using the same energy-detection algorithm/parameters as vad, or sensible
// defaults (ThresholdRMS 300, matching DefaultVAD) if vad is nil -- e.g. no
// VAD is configured for suppression bypass, but TurnEnd still needs its own
// energy detector to find the "energy drop".
//
// HangoverFrames is always forced to 0 on the clone: TurnEnd's own
// turnEndSilenceFrameThreshold is the sole cushion against premature
// end-of-utterance firing, so double-applying the bypass VAD's hangover
// window on top of it would silently inflate the effective silence
// requirement past 200ms.
func cloneVADForTurnEnd(vad VADer) VADer {
	switch v := vad.(type) {
	case *VAD:
		cp := *v
		cp.HangoverFrames = 0
		cp.hangover = 0
		return &cp
	case *AdaptiveVAD:
		cp := *v
		cp.HangoverFrames = 0
		cp.hangover = 0
		// Start calibration fresh: this is a new, independent instance.
		cp.calibrated = false
		cp.frameCount = 0
		cp.rmsWindow = nil
		cp.totalFrames = 0
		cp.speechFrames = 0
		return &cp
	default:
		return &VAD{ThresholdRMS: 300, HangoverFrames: 0}
	}
}

// observe feeds one frame's PCM samples into the turn-end state machine. It
// must be called at most once per processed 10ms frame. Safe to call on a
// nil *turnEndTracker (no-op), so call sites do not need their own nil check.
func (t *turnEndTracker) observe(frame []int16) {
	if t == nil {
		return
	}
	isSpeech := true
	if t.vad != nil {
		isSpeech = t.vad.IsSpeech(frame)
	}
	if isSpeech {
		t.sawSpeech = true
		t.silenceFrames = 0
		return
	}
	if !t.sawSpeech {
		// No speech observed yet since the last turn-end/reset -- leading
		// silence never constitutes an utterance boundary.
		return
	}
	t.silenceFrames++
	if t.silenceFrames < turnEndSilenceFrameThreshold {
		return
	}
	event := TurnEndEvent{
		Timestamp: time.Now(),
		SilenceMs: t.silenceFrames * turnEndFrameDurationMs,
	}
	select {
	case t.ch <- event:
	default:
		// Full: drop the oldest queued turn-end event to make room for the
		// newest one -- mirrors CleanAudio's drop-oldest-on-full policy in
		// pkg/rtp.Session; a consumer that has fallen behind should still
		// learn about the most recent turn boundary, not a stale one.
		select {
		case <-t.ch:
		default:
		}
		select {
		case t.ch <- event:
		default:
			// Consumer drained and refilled between our two selects (rare
			// race) -- drop this event rather than block the pipeline.
		}
	}
	// Require a fresh speech run before the next turn-end can fire, so
	// continued silence after this event does not re-trigger every frame.
	t.sawSpeech = false
	t.silenceFrames = 0
}

// setThreshold updates the energy threshold on the tracker's own VAD clone,
// mirroring a Pipeline.SetVADThreshold call on the pipeline's suppression-
// bypass VAD. Without this, TurnEnd's independent, hangover-free clone (see
// cloneVADForTurnEnd) would keep using the threshold captured at
// NewPipeline/newTurnEndTracker time for the rest of the call, silently
// drifting out of sync with the bypass gate's live sensitivity. Safe to call
// on a nil *turnEndTracker (no-op), matching every other method here.
func (t *turnEndTracker) setThreshold(threshold float64) {
	if t == nil {
		return
	}
	if vad, ok := t.vad.(*VAD); ok {
		vad.ThresholdRMS = threshold
	}
	if avad, ok := t.vad.(*AdaptiveVAD); ok {
		avad.VAD.ThresholdRMS = threshold
	}
}

// reset clears per-utterance detection state. Called from Pipeline.Reset().
// It does not close the channel: Reset() is for reusing the pipeline across
// a new stream/file, not for tearing it down -- see Pipeline.Close() for that.
// Safe to call on a nil *turnEndTracker (no-op) and safe to call repeatedly.
func (t *turnEndTracker) reset() {
	if t == nil {
		return
	}
	if t.vad != nil {
		t.vad.Reset()
	}
	t.sawSpeech = false
	t.silenceFrames = 0
}

// close closes the event channel exactly once, guarded by sync.Once so it is
// safe to call multiple times and from multiple goroutines. Safe to call on
// a nil *turnEndTracker (no-op).
func (t *turnEndTracker) close() {
	if t == nil {
		return
	}
	t.closeOnce.Do(func() { close(t.ch) })
}
