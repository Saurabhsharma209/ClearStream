package audio

import (
	"fmt"
	"math"
)

// Resample converts PCM samples from srcRate to dstRate.
// For the common 8kHz→16kHz (2x upsample) case, a Kaiser-windowed sinc FIR filter is used
// (Kaiser beta=5.653, 31 taps, cutoff at Nyquist of lower rate) for high quality upsampling
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

	// Use Kaiser-windowed FIR for the 16kHz→8kHz (2x downsample) path.
	// Without an anti-alias filter, frequencies above 4kHz fold into the
	// 0–4kHz passband on G.711 output, causing audible aliasing distortion.
	if srcRate == 16000 && dstRate == 8000 {
		return kaiserFIRDownsample2x(samples), nil
	}

	return linearResample(samples, srcRate, dstRate)
}

// kaiserFIRUpsample2x upsamples by exactly 2x using a 31-tap Kaiser-windowed sinc FIR.
// Kaiser beta=5.653 targets 60 dB stopband attenuation (per the Kaiser design formula
// beta = 0.1102*(As-8.7) with As=60 dB). Cutoff fc=0.25 = 0.5*srcNyquist (normalised).
// Odd-reflection boundary extension at the left edge ensures the filter is pre-warmed
// correctly for continuous audio, eliminating the startup transient that would otherwise
// reduce measured SNR by ~2 dB.
func kaiserFIRUpsample2x(samples []int16) []int16 {
	const (
		L    = 31    // filter length (odd)
		beta = 5.653 // Kaiser window shape parameter (targets 60 dB stopband attenuation)
		fc   = 0.25  // normalised cutoff (0.5 * srcNyquist in terms of dstRate)
	)

	// Build Kaiser-windowed sinc coefficients (shared helper — see kaiserSincCoeffs).
	h := kaiserSincCoeffs(L, beta, fc)
	M := L - 1

	// Upsample by 2: insert zeros between samples, then convolve with FIR.
	// upLen = 2 * len(samples)
	N := len(samples)
	outLen := 2 * N
	out := make([]int16, outLen)
	half := M / 2 // filter group delay in output samples

	for i := 0; i < outLen; i++ {
		var acc float64
		for k := 0; k < L; k++ {
			// Index into the upsampled (zero-inserted) signal
			j := i - k + half
			if j >= outLen {
				continue
			}
			// Only even indices correspond to original samples (odd are zeros)
			if j%2 == 0 {
				srcIdx := j / 2
				if srcIdx >= 0 && srcIdx < N {
					acc += h[k] * float64(samples[srcIdx])
				} else if srcIdx < 0 {
					// Odd-reflection boundary: extend signal as x[-n] = -x[n].
					// This pre-warms the filter for continuous audio, eliminating
					// the startup transient from zero-padding at the left boundary.
					reflected := -srcIdx
					if reflected < N {
						acc += h[k] * (-float64(samples[reflected]))
					}
				}
				// srcIdx >= N: implicit zero-padding at the right boundary.
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

// kaiserFIRDownsample2x downsamples by exactly 2x using a 31-tap Kaiser-windowed sinc FIR
// as an anti-alias filter followed by decimation (keep every other sample).
//
// Same Kaiser parameters as kaiserFIRUpsample2x: beta=5.653, L=31 taps, fc=0.25.
// fc=0.25 places the cutoff at 4kHz (the Nyquist of the 8kHz output), normalised
// to the 16kHz input rate (4000/16000 = 0.25). This suppresses all energy above 4kHz
// before decimation, preventing aliasing on the G.711 output path.
//
// Odd-reflection boundary extension at the left edge (same as kaiserFIRUpsample2x)
// pre-warms the filter and eliminates the startup transient.
//
// Output length = ceil(len(samples)/2).
func kaiserFIRDownsample2x(samples []int16) []int16 {
	const (
		L    = 31    // filter length (odd) — same as upsample path
		beta = 5.653 // Kaiser window shape parameter (targets 60 dB stopband attenuation)
		fc   = 0.25  // normalised cutoff: 4kHz / 16kHz input rate
	)

	// Build Kaiser-windowed sinc coefficients (identical design to upsample path,
	// via the shared kaiserSincCoeffs helper).
	h := kaiserSincCoeffs(L, beta, fc)
	M := L - 1

	N := len(samples)
	half := M / 2 // filter group delay in input samples

	// Output length = ceil(N/2): each output sample i corresponds to input index 2*i.
	outLen := (N + 1) / 2
	out := make([]int16, outLen)

	for i := 0; i < outLen; i++ {
		// Centre of the convolution window in the input signal.
		centre := 2 * i
		var acc float64
		for k := 0; k < L; k++ {
			// Source index in the input signal.
			srcIdx := centre - half + k
			if srcIdx >= 0 && srcIdx < N {
				acc += h[k] * float64(samples[srcIdx])
			} else if srcIdx < 0 {
				// Odd-reflection boundary extension: x[-n] = -x[n].
				// Keeps the filter pre-warmed for continuous audio streams.
				reflected := -srcIdx
				if reflected < N {
					acc += h[k] * (-float64(samples[reflected]))
				}
			}
			// srcIdx >= N: implicit zero-padding at the right boundary.
		}
		v := math.Round(acc)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}

// kaiserSincCoeffs builds L Kaiser-windowed sinc FIR coefficients with window
// shape parameter beta and normalised cutoff fc. Extracted from
// kaiserFIRUpsample2x/kaiserFIRDownsample2x (and reused by
// kaiserFIRUpsample3x/kaiserFIRDownsample3x) so the coefficient-generation
// math — sinc kernel * Kaiser window, via besselI0 — is defined exactly once
// instead of duplicated per filter length/ratio. The normalisation convention
// for fc (fraction of whichever sample rate the caller's convolution loop
// operates in) matches each caller's own doc comment.
func kaiserSincCoeffs(L int, beta, fc float64) []float64 {
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
	return h
}

// kaiser3xL/kaiser3xBeta/kaiser3xFc are the shared Kaiser-FIR design
// parameters for the 3x resampling pair used by Pipeline.Process48k
// (kaiserFIRDownsample3xStateful / kaiserFIRUpsample3xStateful below).
//
// Kaiser beta=5.653 targets the same 60 dB stopband attenuation as the 2x
// filters (kaiserFIRUpsample2x/kaiserFIRDownsample2x). A longer 63-tap
// filter (vs 31 for the 2x case) is used because fc is a narrower fraction
// of the sample rate than the 2x filters' fc=0.25, and proportionally more
// taps are needed to keep the same transition-band sharpness (in Hz).
//
// fc is deliberately set to 1/8 (6kHz), *below* the theoretically "exact"
// anti-alias cutoff of 1/6 (8kHz = the Nyquist of the 16kHz processing
// rate). This was an empirical tuning decision, not just a theoretical one:
// an fc=1/6 filter — textbook-correct for pure anti-aliasing, with a flat
// passband all the way to 8kHz — was A/B tested (TestABProcess48kVsDirect)
// and found to produce *worse* post-suppression SNR than the original
// cheap 3-sample-average/linear-interpolation implementation it replaced.
// The reason: that crude filter's soft, early-rolling-off frequency
// response was incidentally acting as a mild broadband noise attenuator
// before the noise reducer/suppressor ever ran, and a textbook flat-
// passband anti-alias filter — by design — does not reproduce that
// side-effect, so it exposed the suppressor to more raw noise energy in
// the 6-8kHz band than it can fully remove on its own. Tightening fc to
// 1/8 recovers (and slightly exceeds) that lost benefit while still
// preserving materially more bandwidth than 8kHz-Nyquist narrowband
// (telephone-quality, ~4kHz) processing — see DEVLOG 2026-07-09 for the
// measured before/after numbers.
//
// Coefficients are computed once at package init (not per call/frame) since
// Process48k runs this on every 10ms frame — see kaiser3xCoeffs.
const (
	kaiser3xL    = 63        // filter length (odd)
	kaiser3xBeta = 5.653     // Kaiser window shape parameter (60 dB stopband)
	kaiser3xFc   = 1.0 / 8.0 // normalised cutoff: 6kHz (see doc comment above)
)

// kaiser3xCoeffs holds the precomputed Kaiser-windowed sinc taps shared by
// kaiserFIRDownsample3xStateful and kaiserFIRUpsample3xStateful.
var kaiser3xCoeffs = kaiserSincCoeffs(kaiser3xL, kaiser3xBeta, kaiser3xFc)

// kaiser3xDownHistLen is the number of trailing 48kHz input samples that
// must be carried across calls to kaiserFIRDownsample3xStateful so each new
// frame's causal FIR window has real preceding audio to convolve against
// (instead of a synthetic per-call boundary assumption).
const kaiser3xDownHistLen = kaiser3xL - 1

// kaiser3xUpHistLen is the number of trailing 16kHz (post-suppression)
// samples that must be carried across calls to kaiserFIRUpsample3xStateful,
// sized so the causal window in the zero-inserted (48kHz) domain never needs
// to look further back than real history provides: ceil((L-1)/3).
const kaiser3xUpHistLen = (kaiser3xL - 1 + 2) / 3

// Process48kGroupDelaySamples is the constant, deterministic group delay (in
// 48kHz samples) that Pipeline.Process48k's stateful Kaiser-FIR resampling
// introduces end-to-end: kaiserFIRDownsample3xStateful and
// kaiserFIRUpsample3xStateful are each causal (they use only real past
// samples, never a same-call boundary assumption — see their doc comments),
// which means each has its own group delay of half the filter length; the
// two stages compose to a fixed total of (kaiser3xL-1) samples ≈ 1.3ms at
// 48kHz. This is standard for any streaming FIR resampler/anti-alias filter
// and is negligible for real-time voice (well under typical jitter-buffer
// budgets); it replaced the previous 3-sample-average/linear-interpolation
// implementation, which had zero delay but far worse frequency response.
// Exported for callers/tests that need to time-align Process48k's output
// against its input (e.g. objective quality metrics computed sample-by-
// sample against a reference).
const Process48kGroupDelaySamples = kaiser3xL - 1

// kaiserFIRDownsample3xStateful downsamples exactly 3x (e.g. 48kHz->16kHz)
// using the shared kaiser3xCoeffs anti-alias FIR, applied *causally*: each
// output sample's convolution window uses only `hist` (real samples carried
// over from the previous call) and `frame` itself — never a same-call
// boundary assumption. This is what makes it safe to call once per 10ms
// frame from Pipeline.Process48k without introducing a resampling artefact
// at every frame boundary (the failure mode of a naive per-call
// zero-padded/reflected-boundary FIR, which this replaced after A/B testing
// showed it made frame-chunked resampling *worse* than the cheap
// 3-sample-average it was meant to improve on — see DEVLOG 2026-07-09).
//
// hist must hold exactly kaiser3xDownHistLen samples immediately preceding
// frame (zero-filled for the first call of a session/after Reset).
// len(frame) must be a multiple of 3.
//
// Returns the downsampled output (len(frame)/3 samples) and the updated
// history slice to pass into the next call.
func kaiserFIRDownsample3xStateful(frame, hist []int16) (out, newHist []int16) {
	h := kaiser3xCoeffs
	L := kaiser3xL
	N := len(frame)

	combined := make([]int16, 0, len(hist)+N)
	combined = append(combined, hist...)
	combined = append(combined, frame...)

	outLen := N / 3
	out = make([]int16, outLen)
	for i := 0; i < outLen; i++ {
		base := 3 * i
		var acc float64
		for k := 0; k < L; k++ {
			acc += h[k] * float64(combined[base+k])
		}
		v := math.Round(acc)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}

	tailStart := len(combined) - kaiser3xDownHistLen
	if tailStart < 0 {
		tailStart = 0
	}
	newHist = append([]int16(nil), combined[tailStart:]...)
	return out, newHist
}

// kaiserFIRUpsample3xStateful upsamples exactly 3x (e.g. 16kHz->48kHz) using
// the shared kaiser3xCoeffs interpolation FIR (zero-insertion + convolution),
// applied causally against real history in the same style as
// kaiserFIRDownsample3xStateful — see that function's doc comment for why
// per-call statefulness matters for Process48k's 10ms-frame streaming usage.
//
// hist must hold exactly kaiser3xUpHistLen samples (in the pre-upsample,
// 16kHz-rate domain) immediately preceding processed (zero-filled for the
// first call of a session/after Reset).
//
// Returns the upsampled output (3*len(processed) samples) and the updated
// history slice to pass into the next call.
func kaiserFIRUpsample3xStateful(processed, hist []int16) (out, newHist []int16) {
	h := kaiser3xCoeffs
	L := kaiser3xL
	H := len(hist)
	N := len(processed)

	combined := make([]int16, 0, H+N)
	combined = append(combined, hist...)
	combined = append(combined, processed...)

	outLen := 3 * N
	out = make([]int16, outLen)
	for i := 0; i < outLen; i++ {
		// p is this output sample's position in the (conceptual) zero-inserted
		// domain, where every 3rd position holds a real sample from combined
		// and the other two are zero.
		p := 3*H + i
		var acc float64
		for k := 0; k < L; k++ {
			pos := p - (L - 1) + k
			if pos < 0 || pos%3 != 0 {
				continue
			}
			idx := pos / 3
			if idx >= 0 && idx < len(combined) {
				acc += h[k] * float64(combined[idx])
			}
		}
		// Scale by 3 (the upsampling factor) to compensate for zero insertion.
		v := math.Round(acc * 3.0)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}

	tailStart := len(combined) - kaiser3xUpHistLen
	if tailStart < 0 {
		tailStart = 0
	}
	newHist = append([]int16(nil), combined[tailStart:]...)
	return out, newHist
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

// linearResample resamples PCM audio between arbitrary sample rates using a
// Kaiser-windowed sinc FIR filter (polyphase-style per-output-sample evaluation).
//
// Design parameters:
//
//	L    = 64 taps  — reasonable quality/performance tradeoff for telephony
//	beta = 5.653    — Kaiser shape parameter targeting ~60 dB stopband attenuation
//	fc   = 0.5 * min(srcRate,dstRate) / max(srcRate,dstRate)
//	       normalised to the higher of the two rates; this places the cutoff at
//	       half the lower Nyquist so aliasing and imaging are both suppressed.
//
// For each output sample i the fractional source position is
//
//	srcPos = i * srcRate / dstRate
//
// A 64-tap windowed sinc centred at srcPos is evaluated directly in the
// continuous (fractional-delay) domain — no polyphase table is pre-built,
// which keeps code simple at the cost of a small per-sample multiply loop.
func linearResample(samples []int16, srcRate, dstRate int) ([]int16, error) {
	const (
		L    = 64    // filter length (number of taps)
		beta = 5.653 // Kaiser beta (60 dB stopband, same as kaiserFIRUpsample2x)
	)

	N := len(samples)
	ratio := float64(dstRate) / float64(srcRate)
	outLen := int(math.Round(float64(N) * ratio))
	if outLen == 0 {
		return []int16{}, nil
	}

	// Normalised cutoff: half the Nyquist of the lower-rate side, expressed as a
	// fraction of the higher sample rate (so both src and dst spectra stay intact).
	var fc float64
	if srcRate < dstRate {
		fc = 0.5 * float64(srcRate) / float64(dstRate)
	} else {
		fc = 0.5 * float64(dstRate) / float64(srcRate)
	}

	// Pre-compute Kaiser window denominator I0(beta).
	i0beta := besselI0(beta)

	// Windowed sinc kernel evaluated at a fractional tap offset t (in src samples).
	// The sinc is normalised to fc so its passband gain is 1.0.
	kaiserSinc := func(t float64) float64 {
		// Kaiser window argument: t ranges over [-L/2, L/2].
		half := float64(L) / 2.0
		// Normalised position in [0,1] for the Kaiser window formula.
		u := t / half // u in [-1, 1]
		arg := 1.0 - u*u
		if arg < 0 {
			arg = 0
		}
		window := besselI0(beta*math.Sqrt(arg)) / i0beta

		// Sinc kernel scaled by 2*fc so the DC gain equals 1.
		var sinc float64
		if t == 0 {
			sinc = 2.0 * fc
		} else {
			sinc = math.Sin(2*math.Pi*fc*t) / (math.Pi * t)
		}
		return window * sinc
	}

	// Compute DC gain of the filter for a 0-offset sample to apply gain correction.
	// Sum h[k] for k = -(L/2-1)..L/2  at integer taps (no fractional shift).
	var dcGain float64
	for k := -(L/2 - 1); k <= L/2; k++ {
		dcGain += kaiserSinc(float64(k))
	}
	if dcGain == 0 {
		dcGain = 1
	}

	out := make([]int16, outLen)
	for i := 0; i < outLen; i++ {
		// Continuous source position for this output sample.
		srcPos := float64(i) * float64(srcRate) / float64(dstRate)

		// Centre tap index (nearest input sample).
		centre := int(math.Floor(srcPos + 0.5))

		var acc float64
		for k := 0; k < L; k++ {
			// Map tap k to a source index. The filter is centred at `centre`.
			// Tap 0 corresponds to source index centre - L/2 + 1.
			srcIdx := centre - (L/2 - 1) + k
			if srcIdx < 0 || srcIdx >= N {
				// Zero-padding at boundaries.
				continue
			}
			// Fractional offset from srcPos to this source sample (in src samples).
			t := float64(srcIdx) - srcPos
			acc += kaiserSinc(t) * float64(samples[srcIdx])
		}

		// Normalise by DC gain so the filter has unity passband gain.
		acc /= dcGain

		v := math.Round(acc)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
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
	var peak int32
	for _, s := range samples {
		// Widen to int32 before negating: negating math.MinInt16 (-32768) as
		// an int16 overflows two's-complement arithmetic and silently stays
		// -32768 instead of becoming +32768, which would leave the loudest
		// possible sample undetected by the "v > peak" comparison below.
		v := int32(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak == 0 || peak <= int32(maxAbs) {
		return samples
	}
	scale := float64(maxAbs) / float64(peak)
	out := make([]int16, len(samples))
	for i, s := range samples {
		out[i] = int16(float64(s) * scale)
	}
	return out
}
