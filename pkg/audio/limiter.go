package audio

// PeakLimiter prevents clipping by tracking the peak sample amplitude and
// applying a gain reduction whenever the peak exceeds ThresholdRMS.
// It can be embedded in any pipeline stage that needs to guard against bursts
// and click events (e.g. DTMF, line impulses, AGC overshoot).
//
// The limiter is designed for telephony-grade PCM (int16, 16 kHz mono) but
// imposes no constraint on frame length.
type PeakLimiter struct {
	// ThresholdRMS is the amplitude ceiling. Frames whose peak exceeds this
	// value are attenuated so the peak lands at ThresholdRMS.
	// Default: 28000 (≈ -1.3 dBFS relative to int16 full-scale 32767).
	ThresholdRMS float64

	// ReleaseCoeff controls how quickly the gain recovers after a loud event.
	// Each sample: peak = peak * ReleaseCoeff.
	// Default: 0.999 (≈ 7 dB/s release at 16 kHz).
	ReleaseCoeff float64

	// peak is the running envelope follower.
	peak float64
}

// NewPeakLimiter returns a PeakLimiter with telephony-tuned defaults.
func NewPeakLimiter() *PeakLimiter {
	return &PeakLimiter{
		ThresholdRMS: 28000,
		ReleaseCoeff: 0.999,
	}
}

// Process applies peak limiting to frame in-place and returns the result.
// The returned slice is always the same length as frame.
func (l *PeakLimiter) Process(frame []int16) []int16 {
	out := make([]int16, len(frame))

	for i, s := range frame {
		// Update running peak with release decay.
		abs := float64(s)
		if abs < 0 {
			abs = -abs
		}
		l.peak *= l.ReleaseCoeff
		if abs > l.peak {
			l.peak = abs
		}

		// Compute gain: reduce only when peak exceeds threshold.
		v := float64(s)
		if l.peak > l.ThresholdRMS {
			v *= l.ThresholdRMS / l.peak
		}

		// Hard clamp to int16 range.
		if v > 32767 {
			v = 32767
		} else if v < -32767 {
			v = -32767
		}
		out[i] = int16(v)
	}
	return out
}

// Reset clears the peak envelope. Call when starting a new audio stream.
func (l *PeakLimiter) Reset() {
	l.peak = 0
}
