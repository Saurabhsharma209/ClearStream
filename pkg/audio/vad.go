package audio

import "math"

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
// During the first CalibrationFrames frames, it measures the background
// noise level and sets ThresholdRMS = noiseFloor * SensitivityFactor.
type AdaptiveVAD struct {
	VAD
	// CalibrationFrames is how many frames to sample before locking threshold.
	// At 10ms/frame: 50 frames = 500ms. Default: 50.
	CalibrationFrames int
	// SensitivityFactor multiplies the measured noise floor to set threshold.
	// Higher = less sensitive (fewer false positives). Default: 3.0.
	SensitivityFactor float64

	calibrated bool
	frameCount int
	noiseAccum float64
}

// DefaultAdaptiveVAD returns an AdaptiveVAD with 500ms calibration window.
func DefaultAdaptiveVAD() *AdaptiveVAD {
	return &AdaptiveVAD{
		VAD: VAD{
			ThresholdRMS:   300, // initial guess, overwritten after calibration
			HangoverFrames: 8,
		},
		CalibrationFrames: 50,
		SensitivityFactor: 3.0,
	}
}

// IsSpeech implements VAD detection with adaptive threshold.
// During calibration it accumulates noise samples and returns false.
// After calibration the threshold is locked and behaves like regular VAD.
func (a *AdaptiveVAD) IsSpeech(frame []int16) bool {
	rms := rmsEnergy(frame)
	if !a.calibrated {
		a.noiseAccum += rms
		a.frameCount++
		if a.frameCount >= a.CalibrationFrames {
			noiseFloor := a.noiseAccum / float64(a.frameCount)
			a.VAD.ThresholdRMS = noiseFloor * a.SensitivityFactor
			a.calibrated = true
		}
		return false // treat as silence during calibration
	}
	return a.VAD.IsSpeech(frame)
}

// IsCalibrated reports whether the noise floor has been measured.
func (a *AdaptiveVAD) IsCalibrated() bool { return a.calibrated }

// NoiseFloor returns the measured background noise RMS (0 if not calibrated).
func (a *AdaptiveVAD) NoiseFloor() float64 {
	if !a.calibrated || a.frameCount == 0 {
		return 0
	}
	return a.noiseAccum / float64(a.frameCount)
}

// Reset clears calibration state — call when switching to a new call.
func (a *AdaptiveVAD) Reset() {
	a.VAD.Reset()
	a.calibrated = false
	a.frameCount = 0
	a.noiseAccum = 0
}
