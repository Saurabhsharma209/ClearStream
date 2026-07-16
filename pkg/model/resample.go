//go:build rnnoise || onnx

package model

// Shared 16kHz<->48kHz resampling helpers used by both the RNNoise CGo
// backend (rnnoise.go, build tag "rnnoise") and the ONNX-based backends
// (rnnoise_onnx.go / deepfilter_onnx.go, build tag "onnx"). Both backends
// need to hand 480-sample @ 48kHz frames to their respective inference
// engines while ClearStream's pipeline operates at 16kHz/160 samples.
//
// This used to be duplicated per-backend, and the two copies drifted:
// the "rnnoise" build got upgraded to Catmull-Rom/Kaiser-sinc filtering
// while the "onnx" build was left on naive linear interpolation and
// box-average decimation (~13dB image rejection, ~20-25dB alias
// attenuation). Consolidating here means every 16kHz<->48kHz backend gets
// the same, better-quality resampling and future upgrades only need to
// happen in one place.

// upsample3x converts 16kHz (160 samples) to 48kHz (480 samples) using
// 4-point Catmull-Rom cubic interpolation. This provides ~40dB image rejection
// vs ~13dB for linear interpolation, preventing spectral images from corrupting
// inference in the 16kHz-48kHz path.
//
// Catmull-Rom coefficients at t=1/3: [-8, 84, 36, -4] / 108
// Catmull-Rom coefficients at t=2/3: [-4, 36, 84, -8] / 108
// (derived from standard [-2,21,9,-1]/27 and [-1,9,21,-2]/27 scaled by 4)
// Verification: -8+84+36-4=108, -4+36+84-8=108
func upsample3x(in []int16) []int16 {
	out := make([]int16, len(in)*3)
	for i := range in {
		p0 := int32(clampIdx(in, i-1))
		p1 := int32(clampIdx(in, i))
		p2 := int32(clampIdx(in, i+1))
		p3 := int32(clampIdx(in, i+2))

		// Original sample at t=0
		out[i*3] = in[i]

		// Interpolated sample at t=1/3
		v1 := (-8*p0 + 84*p1 + 36*p2 - 4*p3) / 108
		if v1 > 32767 {
			v1 = 32767
		} else if v1 < -32768 {
			v1 = -32768
		}
		out[i*3+1] = int16(v1)

		// Interpolated sample at t=2/3
		v2 := (-4*p0 + 36*p1 + 84*p2 - 8*p3) / 108
		if v2 > 32767 {
			v2 = 32767
		} else if v2 < -32768 {
			v2 = -32768
		}
		out[i*3+2] = int16(v2)
	}
	return out
}

// downsample3x converts 48kHz (480 samples) to 16kHz (160 samples) using a
// 15-tap Kaiser-windowed sinc FIR before decimation by 3.
//
// Design: fc=1/6 (8kHz/48kHz), Kaiser beta=5.653, L=15 taps.
// h[n] = sinc(2*fc*(n-M/2)) * kaiser(n, beta, M), M=14, scaled so sum=256.
//
// Integer coefficients (symmetric, scale=256):
//
//	[0, 0, -3, -7, 0, 28, 67, 85, 67, 28, 0, -7, -3, 0, 0]
//	Sum=255, DC gain=255/256=0.9961 (~0dB).
//
// Stopband attenuation: null at 16kHz (1/3*Fs), >=44dB across alias band
// [16kHz,24kHz], vs ~20-25dB for a naive box-average decimator. Prevents
// high-frequency content (>8kHz) from aliasing into the speech band.
func downsample3x(in []int16) []int16 {
	n := len(in)
	out := make([]int16, n/3)
	// 15-tap Kaiser-windowed sinc coefficients, scaled for >>8 fixed-point.
	// Symmetric: h[0]=h[14]=0, h[1]=h[13]=0, h[2]=h[12]=-3, h[3]=h[11]=-7,
	//            h[4]=h[10]=0, h[5]=h[9]=28, h[6]=h[8]=67, h[7]=85 (center).
	const (
		hB = -3 // taps 2,12
		hC = -7 // taps 3,11
		hE = 28 // taps 5,9
		hF = 67 // taps 6,8
		hG = 85 // tap  7 (center)
	)
	for i := range out {
		c := i * 3 // centre tap index in 48kHz input
		// Gather 15 input samples centered on c, with boundary clamping.
		// Taps 0,1,13,14 have coefficient 0 and are omitted.
		s2 := int32(clampIdx(in, c-5))
		s3 := int32(clampIdx(in, c-4))
		s5 := int32(clampIdx(in, c-2))
		s6 := int32(clampIdx(in, c-1))
		s7 := int32(in[c]) // centre tap -- always valid
		s8 := int32(clampIdx(in, c+1))
		s9 := int32(clampIdx(in, c+2))
		s11 := int32(clampIdx(in, c+4))
		s12 := int32(clampIdx(in, c+5))
		// Exploit symmetry: pair taps h[n] == h[14-n].
		acc := hB*(s2+s12) + hC*(s3+s11) + hE*(s5+s9) + hF*(s6+s8) + hG*s7
		out[i] = int16(acc >> 8) // divide by 256
	}
	return out
}

// clampIdx returns in[i], clamping i to [0, len(in)-1].
func clampIdx(in []int16, i int) int16 {
	if i < 0 {
		return in[0]
	}
	if i >= len(in) {
		return in[len(in)-1]
	}
	return in[i]
}
