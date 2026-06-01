package audio

import (
	"fmt"
	"math"
)

// Resample converts PCM samples from srcRate to dstRate.
// For the common 8kHz→16kHz (2x upsample) case, a Kaiser-windowed sinc FIR filter is used
// (Kaiser beta=5.0, 31 taps, cutoff at Nyquist of lower rate) for high quality upsampling
// critical for G.711 call audio. All other ratios fall back to linear interpolation.
func Resample(samples []int16, srcRate, dstRate int) ([]int16, error) {
	if srcRate <= 0 || dstRate <= 0 {
		return nil, fmt.Errorf("resample: invalid rates src=%d dst=%d", srcRate, dstRate)
	}
	if srcRate == dstRate {
		return samples, nil
	}

	// Use Kaiser-windowed FIR for the critical 8kHz→16kHz (2x upsample) path.
	if srcRate == 8000 && dstRate == 16000 {
		return kaiserFIRUpsample2x(samples), nil
	}

	return linearResample(samples, srcRate, dstRate)
}

// kaiserFIRUpsample2x upsamples by exactly 2x using a 31-tap Kaiser-windowed sinc FIR.
// Kaiser beta=5.0 gives ~60 dB stopband attenuation; cutoff = 0.5 * srcNyquist (normalised 0.25).
func kaiserFIRUpsample2x(samples []int16) []int16 {
	const (
		L    = 31   // filter length (odd)
		beta = 5.0  // Kaiser window shape parameter
		fc   = 0.25 // normalised cutoff (0.5 * srcNyquist in terms of dstRate)
	)

	// Build Kaiser-windowed sinc coefficients.
	h := make([]float64, L)
	M := L - 1
	i0beta := besselI0(beta)
	for n := 0; n < L; n++ {
		t := float64(n) - float64(M)/2.0
		// Sinc kernel
		var sinc float64
		if t == 0 {
			sinc = 2.0 * fc
		} else {
			sinc = math.Sin(2*math.Pi*fc*t) / (math.Pi * t)
		}
		// Kaiser window
		arg := 1.0 - (2.0*float64(n)/float64(M)-1.0)*(2.0*float64(n)/float64(M)-1.0)
		if arg < 0 {
			arg = 0
		}
		window := besselI0(beta*math.Sqrt(arg)) / i0beta
		h[n] = sinc * window
	}

	// Upsample by 2: insert zeros between samples, then convolve with FIR.
	// upLen = 2 * len(samples)
	outLen := 2 * len(samples)
	out := make([]int16, outLen)
	half := M / 2 // filter group delay in output samples

	for i := 0; i < outLen; i++ {
		var acc float64
		for k := 0; k < L; k++ {
			// Index into the upsampled (zero-inserted) signal
			j := i - k + half
			if j < 0 || j >= outLen {
				continue
			}
			// Only even indices correspond to original samples (odd are zeros)
			if j%2 == 0 {
				srcIdx := j / 2
				if srcIdx >= 0 && srcIdx < len(samples) {
					acc += h[k] * float64(samples[srcIdx])
				}
			}
		}
		// Scale by 2 (the upsampling factor) to compensate for zero insertion
		v := math.Round(acc * 2.0)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}

// besselI0 computes the modified Bessel function of the first kind, order 0, I_0(x).
// Used for Kaiser window computation.
func besselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	for k := 1; k <= 30; k++ {
		term *= (x / 2.0) / float64(k)
		term2 := term * term
		sum += term2
		if term2 < 1e-15*sum {
			break
		}
	}
	return sum
}

// linearResample is a fallback for arbitrary rate conversions using linear interpolation.
func linearResample(samples []int16, srcRate, dstRate int) ([]int16, error) {
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
