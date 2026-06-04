package audio

import "math"

// AdaptiveNoiseReducer implements multi-band Wiener gain noise reduction with
// per-band noise floor tracking. It operates on 160-sample frames (10ms @
// 16kHz) and requires no external dependencies.
//
// Algorithm:
//  1. Split each 160-sample frame into 8 sub-bands of 20 samples each.
//  2. Compute RMS for each band.
//  3. Track per-band noise floor via EMA: rises slowly during speech,
//     falls toward quiet-frame RMS during silence.
//  4. Apply Wiener gain per band: gain = max(MinGain, 1 - OversubFactor*floor/rms).
//  5. After reassembly apply a soft gate: attenuate by 0.1x frames whose
//     RMS is below 1.5x the global noise EMA (these are pure-noise frames).
type AdaptiveNoiseReducer struct {
	// Per-band noise floor tracker (8 bands via sub-band RMS)
	bandNoiseFloor  [8]float64 // EMA of noise floor per band
	bandSignalLevel [8]float64 // EMA of signal level per band

	// Global noise floor EMA
	globalNoiseEMA float64
	frameCount     int

	// Config — exported so callers can tune after construction.
	AttackCoeff   float64 // how fast noise floor rises (default 0.02)
	ReleaseCoeff  float64 // how fast noise floor falls (default 0.995)
	OversubFactor float64 // oversubtraction factor 1.0–2.0 (default 1.5)
	MinGain       float64 // minimum gain floor (default 0.05 ≈ -26 dB)
	SpeechThresh  float64 // RMS threshold separating speech from noise (default 200)

	// Peak limiter state
	peakHold float64
}

const (
	nrBands    = 8
	nrBandSize = FrameSizeSamples / nrBands // 20 samples per band
)

// NewAdaptiveNoiseReducer returns an AdaptiveNoiseReducer with telephony-tuned
// defaults. No external dependencies required.
func NewAdaptiveNoiseReducer() *AdaptiveNoiseReducer {
	return &AdaptiveNoiseReducer{
		AttackCoeff:   0.02,
		ReleaseCoeff:  0.995,
		OversubFactor: 1.5,
		MinGain:       0.05,
		SpeechThresh:  200,
	}
}

// Name returns the processor identifier.
func (r *AdaptiveNoiseReducer) Name() string { return "adaptive-nr" }

// Process applies multi-band Wiener gain noise reduction to frame and returns
// the cleaned samples. The output slice is always len(frame) samples.
func (r *AdaptiveNoiseReducer) Process(frame []int16) ([]int16, error) {
	if len(frame) == 0 {
		return frame, nil
	}

	// Work in float64 for precision; convert back at the end.
	fIn := make([]float64, len(frame))
	for i, s := range frame {
		fIn[i] = float64(s)
	}

	// --- Sub-band processing ---
	// We process as many complete bands as fit in the frame; leftover samples
	// (if frame length is not a multiple of nrBandSize) pass through unchanged.
	nBands := len(frame) / nrBandSize
	if nBands > nrBands {
		nBands = nrBands
	}

	bandGain := [nrBands]float64{}
	bandRMS := [nrBands]float64{}

	for b := 0; b < nBands; b++ {
		start := b * nrBandSize
		end := start + nrBandSize

		// Compute RMS for this band.
		var sumSq float64
		for _, v := range fIn[start:end] {
			sumSq += v * v
		}
		rms := math.Sqrt(sumSq / float64(nrBandSize))
		bandRMS[b] = rms

		// Update noise floor EMA.
		floor := r.bandNoiseFloor[b]
		if rms < r.SpeechThresh {
			// Quiet frame: noise floor tracks toward rms (fall = release).
			floor = floor*r.ReleaseCoeff + rms*(1-r.ReleaseCoeff)
		} else {
			// Speech frame: noise floor rises slowly (attack).
			floor = floor * (1 - r.AttackCoeff)
		}
		r.bandNoiseFloor[b] = floor

		// Update signal level EMA (fast attack, slow release).
		r.bandSignalLevel[b] = r.bandSignalLevel[b]*0.9 + rms*0.1

		// Wiener gain for this band.
		denom := rms
		if denom < 1 {
			denom = 1
		}
		g := 1 - r.OversubFactor*floor/denom
		if g < r.MinGain {
			g = r.MinGain
		}
		bandGain[b] = g
	}

	// Apply per-band gain.
	fOut := make([]float64, len(fIn))
	copy(fOut, fIn) // copy remainder (beyond processed bands) unchanged
	for b := 0; b < nBands; b++ {
		start := b * nrBandSize
		end := start + nrBandSize
		g := bandGain[b]
		for i := start; i < end; i++ {
			fOut[i] = fIn[i] * g
		}
	}

	// --- Global noise floor tracking ---
	var sumSqAll float64
	for _, v := range fOut {
		sumSqAll += v * v
	}
	frameRMS := math.Sqrt(sumSqAll / float64(len(fOut)))

	r.frameCount++
	if r.frameCount == 1 {
		// Bootstrap the global EMA on the very first frame.
		r.globalNoiseEMA = frameRMS
	} else {
		// Slow EMA: tracks the quietest sustained level.
		if frameRMS < r.globalNoiseEMA {
			r.globalNoiseEMA = r.globalNoiseEMA*0.99 + frameRMS*0.01
		} else {
			r.globalNoiseEMA = r.globalNoiseEMA*0.9995 + frameRMS*0.0005
		}
	}

	// --- Soft gate ---
	// If frame RMS is below 1.5× global noise floor, this is a noise-only
	// frame: attenuate to 10% rather than passing it through.
	gateThresh := r.globalNoiseEMA * 1.5
	if frameRMS < gateThresh {
		for i := range fOut {
			fOut[i] *= 0.1
		}
	}

	// Convert back to int16 with clamping.
	out := make([]int16, len(frame))
	for i, v := range fOut {
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out, nil
}

// Reset clears all internal state. Call this when switching to a new audio
// stream so the noise floor estimate starts fresh.
func (r *AdaptiveNoiseReducer) Reset() {
	for i := range r.bandNoiseFloor {
		r.bandNoiseFloor[i] = 0
		r.bandSignalLevel[i] = 0
	}
	r.globalNoiseEMA = 0
	r.frameCount = 0
	r.peakHold = 0
}
