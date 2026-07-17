package audio

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// SpeakerLabel identifies a speaker in a diarized segment.
type SpeakerLabel string

const (
	SpeakerNearEnd SpeakerLabel = "near"    // local microphone / agent
	SpeakerFarEnd  SpeakerLabel = "far"     // remote caller / customer
	SpeakerSilence SpeakerLabel = "silence" // no active speaker
	SpeakerUnknown SpeakerLabel = "unknown" // cannot determine
)

// DiarizedSegment represents a time-stamped speaker segment.
type DiarizedSegment struct {
	Speaker   SpeakerLabel
	StartMs   int64   // milliseconds from session start
	EndMs     int64   // milliseconds from session start (-1 = ongoing)
	EnergyRMS float64 // mean RMS energy of this segment (0-1 normalized)
}

// String returns a human-readable representation.
func (s DiarizedSegment) String() string {
	dur := s.EndMs - s.StartMs
	if s.EndMs < 0 {
		return fmt.Sprintf("[%s] %dms-ongoing (rms=%.3f)", s.Speaker, s.StartMs, s.EnergyRMS)
	}
	return fmt.Sprintf("[%s] %dms-%dms (%dms, rms=%.3f)", s.Speaker, s.StartMs, s.EndMs, dur, s.EnergyRMS)
}

// Diarizer is the interface for speaker diarization engines.
// Implementations may be energy-based (this file), ML-based (future), or cloud-based.
type Diarizer interface {
	// ProcessFrame classifies a 10ms PCM frame and updates internal state.
	// Returns the current speaker label for this frame.
	ProcessFrame(samples []int16, frameMs int64) SpeakerLabel

	// Segments returns all completed (non-ongoing) speaker segments so far.
	Segments() []DiarizedSegment

	// CurrentSegment returns the active (ongoing) segment. EndMs == -1.
	CurrentSegment() *DiarizedSegment

	// Reset clears all state (call on new call leg).
	Reset()
}

// FarEndAwareDiarizer is implemented by Diarizers that support two-channel
// diarization via a far-end reference signal. Pipeline type-asserts against
// this interface (rather than adding the method to Diarizer itself) so that
// single-channel Diarizer implementations are never forced to implement a
// method they have no use for. EnergyDiarizer.SetFarEndRMS is the reference
// implementation -- see its doc comment for the near/far attribution rule.
type FarEndAwareDiarizer interface {
	Diarizer
	SetFarEndRMS(farEndSamples []int16)
}

// EnergyDiarizerConfig configures the energy-based diarizer.
type EnergyDiarizerConfig struct {
	// SilenceThreshold is the normalized RMS below which a frame is silence. Default: 0.01
	SilenceThreshold float64

	// SpeakerChangeGapMs is the minimum silence gap (ms) that marks a speaker turn. Default: 300
	SpeakerChangeGapMs int64

	// SampleRate is the PCM sample rate. Default: 16000
	SampleRate int
}

// DefaultEnergyDiarizerConfig returns defaults suitable for 16kHz telephony.
func DefaultEnergyDiarizerConfig() EnergyDiarizerConfig {
	return EnergyDiarizerConfig{
		SilenceThreshold:   0.01,
		SpeakerChangeGapMs: 300,
		SampleRate:         16000,
	}
}

// EnergyDiarizer is a simple energy-based speaker turn detector.
// It tracks RMS energy per frame and detects speaker changes via silence gaps.
//
// By default it is single-channel -- it uses the near-end mic only, and every
// active frame is attributed to SpeakerNearEnd. Call SetFarEndRMS once per
// frame (before ProcessFrame) with the same far-end reference passed to
// Pipeline.SetFarEnd/AEC.Process to enable full two-channel diarization:
// frames where the far-end reference is active and the near-end mic is not
// will then be attributed to SpeakerFarEnd instead of silently staying
// SpeakerSilence.
type EnergyDiarizer struct {
	cfg            EnergyDiarizerConfig
	mu             sync.Mutex
	segments       []DiarizedSegment
	current        DiarizedSegment
	silStartMs     int64 // when current silence run started (-1 if not in silence)
	sessionStartMs int64
	started        bool
	farEndRMS      float64 // most recent far-end reference RMS, set via SetFarEndRMS
}

// NewEnergyDiarizer creates a new energy-based speaker diarizer.
func NewEnergyDiarizer(cfg EnergyDiarizerConfig) *EnergyDiarizer {
	if cfg.SilenceThreshold <= 0 {
		cfg.SilenceThreshold = 0.01
	}
	if cfg.SpeakerChangeGapMs <= 0 {
		cfg.SpeakerChangeGapMs = 300
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000
	}
	d := &EnergyDiarizer{cfg: cfg, silStartMs: -1}
	d.current = DiarizedSegment{Speaker: SpeakerSilence, StartMs: 0, EndMs: -1}
	return d
}

// rms computes the normalized RMS energy of a PCM frame.
func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s) / 32768.0
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// SetFarEndRMS records the far-end reference signal's energy for the frame
// about to be passed to ProcessFrame, enabling two-channel speaker
// attribution. Pass the same far-end PCM reference used for AEC (see
// Pipeline.SetFarEnd). Safe for concurrent use; call at most once per 10ms
// frame, before ProcessFrame.
//
// Near-end mic energy always takes priority when both channels are active:
// AEC has already suppressed far-end leakage into the near-end signal by the
// time it reaches the diarizer, so a frame with active near-end energy is
// attributed to SpeakerNearEnd even during nominal double-talk. Only when the
// near-end mic is below SilenceThreshold does an active far-end reference
// produce SpeakerFarEnd instead of SpeakerSilence.
func (d *EnergyDiarizer) SetFarEndRMS(farEndSamples []int16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.farEndRMS = rms(farEndSamples)
}

// ProcessFrame classifies a frame and updates diarizer state.
func (d *EnergyDiarizer) ProcessFrame(samples []int16, frameMs int64) SpeakerLabel {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		d.sessionStartMs = frameMs
		d.started = true
		d.current.StartMs = frameMs
	}

	nearEnergy := rms(samples)
	nearActive := nearEnergy >= d.cfg.SilenceThreshold
	farActive := d.farEndRMS >= d.cfg.SilenceThreshold

	var label SpeakerLabel
	var energy float64
	switch {
	case nearActive:
		label = SpeakerNearEnd
		energy = nearEnergy
	case farActive:
		label = SpeakerFarEnd
		energy = d.farEndRMS
	default:
		label = SpeakerSilence
	}

	if label != SpeakerSilence {
		d.silStartMs = -1
		if d.current.Speaker != label {
			// transition: silence -> speech, or a direct near<->far handover
			d.current.EndMs = frameMs
			d.current.EnergyRMS = energy
			d.segments = append(d.segments, d.current)
			d.current = DiarizedSegment{Speaker: label, StartMs: frameMs, EndMs: -1}
		}
		d.current.EnergyRMS = energy
		return label
	}

	// Silence frame (neither near nor far reference active)
	if d.silStartMs < 0 {
		d.silStartMs = frameMs
	}
	silDur := frameMs - d.silStartMs
	if silDur >= d.cfg.SpeakerChangeGapMs && d.current.Speaker != SpeakerSilence {
		// Long enough silence -> mark end of speech segment
		d.current.EndMs = d.silStartMs
		d.segments = append(d.segments, d.current)
		d.current = DiarizedSegment{Speaker: SpeakerSilence, StartMs: d.silStartMs, EndMs: -1}
	}
	return SpeakerSilence
}

// Segments returns completed (closed) diarization segments.
func (d *EnergyDiarizer) Segments() []DiarizedSegment {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]DiarizedSegment, len(d.segments))
	copy(cp, d.segments)
	return cp
}

// CurrentSegment returns the currently active (open) segment.
func (d *EnergyDiarizer) CurrentSegment() *DiarizedSegment {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := d.current
	return &cp
}

// Reset clears all state.
func (d *EnergyDiarizer) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.segments = d.segments[:0]
	d.current = DiarizedSegment{Speaker: SpeakerSilence, StartMs: 0, EndMs: -1}
	d.silStartMs = -1
	d.started = false
	d.farEndRMS = 0
}

// SpeakerStats summarizes diarization results.
type SpeakerStats struct {
	TotalMs   int64
	SpeechMs  int64
	SilenceMs int64
	Turns     int
	AvgTurnMs float64
}

// Stats returns a summary of the diarization session.
func (d *EnergyDiarizer) Stats(nowMs int64) SpeakerStats {
	segs := d.Segments()
	var stats SpeakerStats
	stats.TotalMs = nowMs - d.sessionStartMs
	for _, s := range segs {
		dur := s.EndMs - s.StartMs
		if s.Speaker == SpeakerSilence {
			stats.SilenceMs += dur
		} else {
			stats.SpeechMs += dur
			stats.Turns++
		}
	}
	if stats.Turns > 0 {
		stats.AvgTurnMs = float64(stats.SpeechMs) / float64(stats.Turns)
	}
	return stats
}

// DiarizeReport returns a human-readable summary.
func DiarizeReport(segs []DiarizedSegment) string {
	if len(segs) == 0 {
		return "no segments"
	}
	out := fmt.Sprintf("%d segments:\n", len(segs))
	for _, s := range segs {
		out += "  " + s.String() + "\n"
	}
	return out
}

// ensure EnergyDiarizer satisfies Diarizer and FarEndAwareDiarizer
var _ Diarizer = (*EnergyDiarizer)(nil)
var _ FarEndAwareDiarizer = (*EnergyDiarizer)(nil)

// timeMs returns current Unix milliseconds (for use in tests / examples).
func timeMs() int64 { return time.Now().UnixMilli() }
