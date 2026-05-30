package audio

import (
	"fmt"
	"math"
)

// Resample converts PCM samples from srcRate to dstRate using linear interpolation.
// For production, replace with a proper polyphase filter (e.g., libsamplerate via CGo).
// Linear interpolation is sufficient for R&D; quality degrades for large ratio changes.
func Resample(samples []int16, srcRate, dstRate int) ([]int16, error) {
	if srcRate <= 0 || dstRate <= 0 {
		return nil, fmt.Errorf("resample: invalid rates src=%d dst=%d", srcRate, dstRate)
	}
	if srcRate == dstRate {
		return samples, nil
	}

	ratio := float64(dstRate) / float64(srcRate)
	outLen := int(math.Round(float64(len(samples)) * ratio))
	out := make([]int16, outLen)

	for i := 0; i < outLen; i++ {
		srcPos := float64(i) / ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		var a, b int16
		a = samples[srcIdx]
		if srcIdx+1 < len(samples) {
			b = samples[srcIdx+1]
		} else {
			b = a
		}
		// Linear interpolation
		out[i] = int16(float64(a)*(1-frac) + float64(b)*frac)
	}

	return out, nil
}

// ToMono downmixes interleaved stereo (or multi-channel) PCM to mono.
func ToMono(samples []int16, channels int) []int16 {
	if channels == 1 {
		return samples
	}
	mono := make([]int16, len(samples)/channels)
	for i := range mono {
		var sum int32
		for c := 0; c < channels; c++ {
			sum += int32(samples[i*channels+c])
		}
		mono[i] = int16(sum / int32(channels))
	}
	return mono
}

// ToStereo duplicates mono PCM to interleaved stereo.
func ToStereo(samples []int16) []int16 {
	out := make([]int16, len(samples)*2)
	for i, s := range samples {
		out[i*2] = s
		out[i*2+1] = s
	}
	return out
}

// Normalize applies gain to prevent clipping after processing.
// Returns samples scaled so the peak does not exceed maxAbs (typically 32000).
func Normalize(samples []int16, maxAbs int16) []int16 {
	var peak int16
	for _, s := range samples {
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	if peak == 0 || peak <= maxAbs {
		return samples
	}
	scale := float64(maxAbs) / float64(peak)
	out := make([]int16, len(samples))
	for i, s := range samples {
		out[i] = int16(float64(s) * scale)
	}
	return out
}
