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
