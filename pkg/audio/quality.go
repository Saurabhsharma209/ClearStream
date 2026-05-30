package audio

import "math"

// SNREstimator estimates Signal-to-Noise Ratio improvement between
// a noisy and a clean audio buffer. Used to validate suppressor quality.
type SNREstimator struct{}

// EstimateSNR computes the SNR of a PCM buffer in dB.
// Higher is better. Typical telephony: 10-20dB noisy, 30-40dB clean.
func (s *SNREstimator) EstimateSNR(signal, noise []int16) float64 {
	if len(signal) == 0 || len(noise) == 0 {
		return 0
	}
	sigPower := power(signal)
	noisePower := power(noise)
	if noisePower == 0 {
		return 60 // effectively silence = very high SNR
	}
	return 10 * math.Log10(sigPower/noisePower)
}

// SNRImprovement measures how much the suppressor improved audio quality.
// Returns dB improvement (positive = better). Typical good suppressor: 5-15dB.
func (s *SNREstimator) SNRImprovement(noisySignal, cleanSignal []int16) float64 {
	if len(noisySignal) == 0 || len(cleanSignal) == 0 {
		return 0
	}
	// Estimate noise as the difference between noisy and clean
	minLen := len(noisySignal)
	if len(cleanSignal) < minLen {
		minLen = len(cleanSignal)
	}
	diff := make([]int16, minLen)
	for i := range diff {
		v := int32(noisySignal[i]) - int32(cleanSignal[i])
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		diff[i] = int16(v)
	}
	snrBefore := s.EstimateSNR(noisySignal[:minLen], diff)
	snrAfter := s.EstimateSNR(cleanSignal[:minLen], diff)
	return snrAfter - snrBefore
}

func power(samples []int16) float64 {
	var sum float64
	for _, s := range samples {
		f := float64(s)
		sum += f * f
	}
	return sum / float64(len(samples))
}
