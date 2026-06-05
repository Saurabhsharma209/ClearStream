package audio

import (
	"math"
	"sort"
)

// VAD performs energy-based voice activity detection.
// It classifies each 10ms PCM frame as speech or silence.
// For R&D this uses RMS energy thresholding, which is fast and works
// well for telephony. A neural VAD (e.g. Silero) can replace this later.
type VAD struct {
	// ThresholdRMS is the RMS energy level below which a frame is silence.
	// Typical values: 200-500 for 16-bit PCM. Default: 300.
	ThresholdRMS float64

	// HangoverFrames is how many silent frames to keep treating as speech
	// after the last speech frame (prevents clipping word endings).
	// Default: 8 (~80ms).
	HangoverFrames int

	hangover int // current hangover counter
}

// DefaultVAD returns a VAD with sensible defaults for telephony.
func DefaultVAD() *VAD {
	return &VAD{
		ThresholdRMS:   300,
		HangoverFrames: 8,
	}
}

// IsSpeech returns true if the frame contains speech energy.
// It applies hangover so trailing silence after speech is still
// treated as speech (prevents word-end clipping).
func (v *VAD) IsSpeech(frame []int16) bool {
	rms := rmsEnergy(frame)
	if rms >= v.ThresholdRMS {
		v.hangover = v.HangoverFrames
		return true
	}
	if v.hangover > 0 {
		v.hangover--
		return true
	}
	return false
}

// Reset clears the hangover state (call when switching audio streams).
func (v *VAD) Reset() {
	v.hangover = 0
}

// rmsEnergy computes the Root Mean Square energy of a PCM frame.
func rmsEnergy(frame []int16) float64 {
	if len(frame) == 0 {
		return 0
	}
	var sum float64
	for _, s := range frame {
		f := float64(s)
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(frame)))
}

// AdaptiveVAD extends VAD with automatic noise floor calibration.
//
// CS-012 fixes (over-bypass on continuous office noise):
//
//  1. SensitivityFactor raised to 4.5 (was 3.0) — a 3× multiplier on an
//     HVAC + keyboard noise floor of ~800 RMS gives threshold=2400, which
//     is still below typical speech (3000–10000 RMS), so speech is caught.
//     At 4.5× the threshold is 3600 — still below loud speech but far above
//     the noise floor, giving a cleaner gate.
//
//  2. Percentile noise floor: calibration now uses the 20th-percentile RMS
//     across the calibration window instead of the mean. In office noise the
//     mean is pulled up by intermittent loud bursts (keystrokes, chairs);
//     the 20th percentile tracks the true steady-state floor, giving a lower
//     and more stable baseline for the multiplier.
//
//  3. MinSpeechMargin: frames are always classified as speech when their RMS
//     exceeds NoiseFloor × MinSpeechMargin, regardless of the threshold.
//     Default 1.5 — a frame 50% above the noise floor is always processed.
//     This prevents the VAD from bypassing frames where speech is buried in
//     continuous noise (the key office-conv failure mode).
//
//  4. QA gate: SpeechRatio() exposes the fraction of frames classified as
//     speech. The QA harness asserts this is within ±20% of static VAD on
//     speech-heavy fixtures (e.g. raw_audio.wav must score ≥ 52% vs 72% static).
type AdaptiveVAD struct {
	VAD
	// CalibrationFrames is how many frames to sample before locking threshold.
	// At 10ms/frame: 50 frames = 500ms. Default: 50.
	CalibrationFrames int

	// SensitivityFactor multiplies the measured noise floor to set threshold.
	// Higher = less sensitive (fewer false positives).
	// CS-012: raised from 3.0 → 4.5 to prevent over-bypass on office noise.
	SensitivityFactor float64

	// MinSpeechMargin is the multiplier above NoiseFloor below which a frame
	// is always sent to the suppressor (never bypassed).
	// Default: 1.5 — a frame 50% above the floor is always processed.
	// CS-012: prevents bypass of speech buried in continuous noise.
	MinSpeechMargin float64

	calibrated bool
	frameCount int
	// rmsWindow stores per-frame RMS values during calibration for percentile calc.
	rmsWindow []float64

	// stats
	totalFrames  int
	speechFrames int
}

// DefaultAdaptiveVAD returns an AdaptiveVAD with 500ms calibration window.
func DefaultAdaptiveVAD() *AdaptiveVAD {
	return &AdaptiveVAD{
		VAD: VAD{
			ThresholdRMS:   300, // initial guess, overwritten after calibration
			HangoverFrames: 8,
		},
		CalibrationFrames: 50,
		SensitivityFactor: 4.5,  // CS-012: was 3.0, raised to 4.5
		MinSpeechMargin:   1.5,  // CS-012: always process if 1.5× above noise floor
	}
}

// IsSpeech implements VAD detection with adaptive threshold.
// During calibration it accumulates noise samples and returns false.
// After calibration the threshold is locked and behaves like regular VAD,
// with the additional MinSpeechMargin floor guard (CS-012).
func (a *AdaptiveVAD) IsSpeech(frame []int16) bool {
	rms := rmsEnergy(frame)
	a.totalFrames++

	if !a.calibrated {
		a.rmsWindow = append(a.rmsWindow, rms)
		a.frameCount++
		if a.frameCount >= a.CalibrationFrames {
			a.VAD.ThresholdRMS = a.computeThreshold()
			a.calibrated = true
		}
		return false // treat as silence during calibration
	}

	// CS-012: MinSpeechMargin floor — never bypass frames significantly above
	// the noise floor, even if below the sensitivity threshold.
	if a.MinSpeechMargin > 0 && rms >= a.noiseFloor()*a.MinSpeechMargin {
		a.VAD.hangover = a.VAD.HangoverFrames
		a.speechFrames++
		return true
	}

	isSpeech := a.VAD.IsSpeech(frame)
	if isSpeech {
		a.speechFrames++
	}
	return isSpeech
}

// computeThreshold derives the calibrated threshold from the collected window.
// CS-012: uses 20th-percentile RMS instead of mean, to avoid office-noise
// bursts (keystrokes, chairs) inflating the noise floor estimate.
func (a *AdaptiveVAD) computeThreshold() float64 {
	if len(a.rmsWindow) == 0 {
		return a.VAD.ThresholdRMS
	}
	sorted := make([]float64, len(a.rmsWindow))
	copy(sorted, a.rmsWindow)
	sort.Float64s(sorted)

	// 20th percentile index.
	idx := int(float64(len(sorted)) * 0.20)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	noiseFloor := sorted[idx]
	threshold := noiseFloor * a.SensitivityFactor

	// Safety floor: never go below 100 RMS (protects against silent rooms).
	if threshold < 100 {
		threshold = 100
	}
	return threshold
}

// noiseFloor returns the 20th-percentile RMS from the calibration window.
func (a *AdaptiveVAD) noiseFloor() float64 {
	if len(a.rmsWindow) == 0 {
		return 0
	}
	sorted := make([]float64, len(a.rmsWindow))
	copy(sorted, a.rmsWindow)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)) * 0.20)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// IsCalibrated reports whether the noise floor has been measured.
func (a *AdaptiveVAD) IsCalibrated() bool { return a.calibrated }

// NoiseFloor returns the measured 20th-percentile background noise RMS (0 if not calibrated).
func (a *AdaptiveVAD) NoiseFloor() float64 {
	if !a.calibrated {
		return 0
	}
	return a.noiseFloor()
}

// SpeechRatio returns the fraction of post-calibration frames classified as speech.
// QA gate: this should be within ±20% of static VAD on speech-heavy fixtures.
func (a *AdaptiveVAD) SpeechRatio() float64 {
	postCalib := a.totalFrames - a.CalibrationFrames
	if postCalib <= 0 {
		return 0
	}
	return float64(a.speechFrames) / float64(postCalib)
}

// Reset clears calibration state — call when switching to a new call.
func (a *AdaptiveVAD) Reset() {
	a.VAD.Reset()
	a.calibrated = false
	a.frameCount = 0
	a.rmsWindow = a.rmsWindow[:0]
	a.totalFrames = 0
	a.speechFrames = 0
}

// QuickVAD returns true if the frame contains speech energy above threshold.
// It is stateless and allocation-free — intended as a pre-pool gate that
// runs before acquiring a Suppressor from the pool (~5 µs per call).
// threshold is the RMS value above which a frame is considered speech;
// pass 0 to use DefaultQuickVADThreshold (200).
func QuickVAD(frame []int16, threshold float64) bool {
	if threshold <= 0 {
		threshold = DefaultQuickVADThreshold
	}
	// compute RMS inline (no alloc)
	var sum float64
	for _, s := range frame {
		f := float64(s)
		sum += f * f
	}
	if len(frame) == 0 {
		return false
	}
	return math.Sqrt(sum/float64(len(frame))) >= threshold
}

const DefaultQuickVADThreshold = 200.0
