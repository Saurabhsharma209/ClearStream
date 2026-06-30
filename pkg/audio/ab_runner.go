package audio

import (
	"math"

	"github.com/exotel/clearstream/pkg/model"
)

// FrameClass classifies a PCM frame by energy level.
type FrameClass int

const (
	FrameSilence    FrameClass = 0 // RMS < SilenceThresh
	FrameBackground FrameClass = 1 // SilenceThresh ≤ RMS < SpeechThresh (babble / noise)
	FrameSpeech     FrameClass = 2 // RMS ≥ SpeechThresh (foreground speaker)
)

// ABConfig configures an ABRunner.
type ABConfig struct {
	// SilenceThresh is the RMS below which a frame is classified as silence.
	SilenceThresh float64 // default 50
	// SpeechThresh is the RMS at or above which a frame is classified as
	// foreground speech. Frames between the two thresholds are background/babble.
	SpeechThresh float64 // default 280 (tuned for raw_audio.wav)
	// SpeechDegradationLimit is the maximum tolerated RMS reduction on speech
	// frames as a fraction of the raw frame RMS. If suppressor B degrades speech
	// beyond this limit, the frame is flagged as a violation (0.05 = 5%).
	SpeechDegradationLimit float64 // default 0.05
}

// DefaultABConfig returns telephony-tuned defaults for raw_audio.wav.
func DefaultABConfig() ABConfig {
	return ABConfig{
		SilenceThresh:          50,
		SpeechThresh:           280,
		SpeechDegradationLimit: 0.05, // 5% max speech RMS reduction
	}
}

// ABFrameResult holds per-frame comparison data for one suppressor pair.
type ABFrameResult struct {
	FrameIdx int
	Class    FrameClass
	RawRMS   float64 // RMS of original frame
	ARMS     float64 // RMS after suppressor A
	BRMS     float64 // RMS after suppressor B
	// SNRDeltaA is the SNR improvement (dB) by suppressor A vs raw (negative = worse).
	SNRDeltaA float64
	// SNRDeltaB is the SNR improvement (dB) by suppressor B vs raw.
	SNRDeltaB float64
	// BViolation is true when suppressor B degrades a speech frame beyond
	// SpeechDegradationLimit.
	BViolation bool
}

// ABSummary aggregates ABFrameResult across all frames.
type ABSummary struct {
	TotalFrames int

	// Per-class frame counts
	SpeechFrames     int
	BackgroundFrames int
	SilenceFrames    int

	// Mean SNR delta per class for each suppressor
	SpeechSNRDeltaA     float64
	SpeechSNRDeltaB     float64
	BackgroundSNRDeltaA float64
	BackgroundSNRDeltaB float64

	// Speech RMS preservation (1.0 = identical to raw; <1 = suppressed)
	SpeechRMSRatioA float64 // mean(ARMS/RawRMS) on speech frames
	SpeechRMSRatioB float64

	// Background RMS reduction (lower = more background removed)
	BackgroundRMSRatioA float64 // mean(ARMS/RawRMS) on background frames
	BackgroundRMSRatioB float64

	// Violation count: frames where B degrades speech beyond the limit
	SpeechViolations int

	NameA string
	NameB string
}

// ABRunner compares two Suppressors frame-by-frame on the same raw audio.
// It does NOT produce audio output — it measures and reports quality deltas.
type ABRunner struct {
	A, B model.Suppressor
	cfg  ABConfig
}

// NewABRunner creates an ABRunner comparing suppressors A and B.
func NewABRunner(a, b model.Suppressor, cfg ABConfig) *ABRunner {
	return &ABRunner{A: a, B: b, cfg: cfg}
}

// ProcessFrame runs one 160-sample frame through both suppressors and returns
// the per-frame result. The raw frame is not modified.
func (r *ABRunner) ProcessFrame(idx int, raw []int16) ABFrameResult {
	rawRMS := rmsF(raw)
	class := r.classify(rawRMS)

	aOut, _ := r.A.Process(raw)
	bOut, _ := r.B.Process(raw)

	aRMS := rmsF(aOut)
	bRMS := rmsF(bOut)

	snrDeltaA := snrDelta(rawRMS, aRMS)
	snrDeltaB := snrDelta(rawRMS, bRMS)

	// Violation: B degrades speech frame RMS by more than limit
	var violation bool
	if class == FrameSpeech && rawRMS > 0 {
		degradation := (rawRMS - bRMS) / rawRMS
		violation = degradation > r.cfg.SpeechDegradationLimit
	}

	return ABFrameResult{
		FrameIdx:   idx,
		Class:      class,
		RawRMS:     rawRMS,
		ARMS:       aRMS,
		BRMS:       bRMS,
		SNRDeltaA:  snrDeltaA,
		SNRDeltaB:  snrDeltaB,
		BViolation: violation,
	}
}

// Summarise aggregates a slice of ABFrameResults into an ABSummary.
func Summarise(results []ABFrameResult, nameA, nameB string) ABSummary {
	s := ABSummary{TotalFrames: len(results), NameA: nameA, NameB: nameB}

	var (
		spSNRA, spSNRB, spRmsA, spRmsB float64
		bgSNRA, bgSNRB, bgRmsA, bgRmsB float64
	)

	for _, r := range results {
		switch r.Class {
		case FrameSpeech:
			s.SpeechFrames++
			spSNRA += r.SNRDeltaA
			spSNRB += r.SNRDeltaB
			if r.RawRMS > 0 {
				spRmsA += r.ARMS / r.RawRMS
				spRmsB += r.BRMS / r.RawRMS
			}
			if r.BViolation {
				s.SpeechViolations++
			}
		case FrameBackground:
			s.BackgroundFrames++
			bgSNRA += r.SNRDeltaA
			bgSNRB += r.SNRDeltaB
			if r.RawRMS > 0 {
				bgRmsA += r.ARMS / r.RawRMS
				bgRmsB += r.BRMS / r.RawRMS
			}
		case FrameSilence:
			s.SilenceFrames++
		}
	}

	if s.SpeechFrames > 0 {
		n := float64(s.SpeechFrames)
		s.SpeechSNRDeltaA = spSNRA / n
		s.SpeechSNRDeltaB = spSNRB / n
		s.SpeechRMSRatioA = spRmsA / n
		s.SpeechRMSRatioB = spRmsB / n
	}
	if s.BackgroundFrames > 0 {
		n := float64(s.BackgroundFrames)
		s.BackgroundSNRDeltaA = bgSNRA / n
		s.BackgroundSNRDeltaB = bgSNRB / n
		s.BackgroundRMSRatioA = bgRmsA / n
		s.BackgroundRMSRatioB = bgRmsB / n
	}
	return s
}

// ---- helpers ----------------------------------------------------------------

func (r *ABRunner) classify(rms float64) FrameClass {
	if rms < r.cfg.SilenceThresh {
		return FrameSilence
	}
	if rms < r.cfg.SpeechThresh {
		return FrameBackground
	}
	return FrameSpeech
}

func rmsF(s []int16) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		f := float64(v)
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(s)))
}

// snrDelta returns the change in perceived SNR after processing.
// Positive = signal improved (lower noise floor), negative = degraded.
// Uses a simple proxy: 20*log10(rawRMS / processedRMS) — positive when
// processedRMS < rawRMS (noise removed).
func snrDelta(rawRMS, processedRMS float64) float64 {
	if processedRMS < 1 {
		processedRMS = 1
	}
	if rawRMS < 1 {
		return 0
	}
	return 20 * math.Log10(rawRMS/processedRMS)
}
