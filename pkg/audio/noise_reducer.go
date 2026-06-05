package audio

import (
	"math"
	"sync/atomic"
)

// AdaptiveNoiseReducer implements a decision-directed Ephraim-Malah Wiener-filter
// noise reducer with temporal gain smoothing. This eliminates the "musical noise"
// / gain-jitter artifact that naive per-frame Wiener filters produce.
//
// Algorithm (Ephraim & Malah 1984, with practical extensions):
//
//  1. Split each 160-sample frame into 8 sub-bands of 20 samples each.
//  2. Compute per-band RMS.
//  3. Track per-band noise floor via EMA updated only on confirmed noise/silence
//     frames (band RMS < SpeechThresh). During speech frames the floor is frozen
//     rather than allowed to rise — this prevents a background voice from
//     gradually being mistaken for "signal".
//  4. Compute a posteriori SNR:  γ = max(0, (rms/floor)² - 1)
//  5. Decision-directed a priori SNR (Ephraim-Malah):
//       ξ = AlphaP * prevGain² * prevSNR  +  (1-AlphaP) * max(0, γ)
//     This smoothes the SNR estimate over time, removing rapid fluctuations.
//  6. Wiener gain: G_raw = ξ / (ξ + OversubFactor)
//  7. Temporal gain smoothing (KEY — eliminates musical noise):
//       G = AlphaG * G_prev  +  (1-AlphaG) * G_raw
//  8. Apply class-dependent gain floor:
//       speech frame → max(G, MinGainSpeech = 0.55)
//       noise frame  → max(G, MinGainNoise  = 0.08)
//  9. Hangover: hold "speech" classification for HangoverFrames after the last
//     frame that actually exceeded SpeechThresh (prevents word-end clipping).
// 10. Soft gate: attenuate pure-noise frames by GateAttenuation (0.08×).
//
// Tuning guide:
//   AlphaG ↑  → smoother gain, more coloration (0.92–0.98)
//   AlphaP ↑  → slower SNR adaptation, better for stationary noise (0.90–0.96)
//   OversubFactor ↓ → less aggressive, preserves soft phonemes (0.7–1.2)
//   SpeechThresh ↑ → more frames classified as noise (tune to your noise floor)
//   MinGainSpeech ↑ → louder noise residual on speech frames (comfort noise)
//   HangoverFrames ↑ → longer word-end protection (8–20 frames = 80–200ms)
type AdaptiveNoiseReducer struct {
	// Per-band state (8 bands)
	bandNoiseFloor [nrBands]float64 // EMA noise floor per band
	bandGainPrev   [nrBands]float64 // previous smoothed gain (for Ephraim-Malah)
	bandSNRPrev    [nrBands]float64 // previous a priori SNR estimate

	// Global state
	globalNoiseEMA float64
	frameCount     int
	hangoverCount  int // frames remaining in speech hangover

	// ---- Tunable parameters (public so callers can adjust) ----

	// AlphaG is the temporal gain smoothing coefficient.
	// Higher = smoother output, slower to react to transients.
	// Default 0.96. Range 0.90–0.98.
	AlphaG float64

	// AlphaP is the a priori SNR smoothing coefficient (Ephraim-Malah α).
	// Higher = slower SNR adaptation (good for stationary noise like HVAC).
	// Default 0.94. Range 0.88–0.96.
	AlphaP float64

	// OversubFactor controls aggressiveness of noise suppression.
	// Higher = more noise removed but more speech coloration.
	// Default 0.85. Range 0.7–1.5.
	OversubFactor float64

	// SpeechThresh is the per-band RMS threshold distinguishing speech from noise.
	// Tune to ~2–3× your measured inter-speech noise RMS.
	// Default 280 (tuned for raw_audio.wav noise RMS of 22.4).
	SpeechThresh float64

	// MinGainSpeech is the minimum Wiener gain applied to speech-classified bands.
	// Prevents over-suppression of soft phonemes; contributes to comfort noise.
	// Default 0.55.
	MinGainSpeech float64

	// MinGainNoise is the minimum Wiener gain applied to noise-classified bands.
	// Default 0.08 (~−22 dB suppression floor).
	MinGainNoise float64

	// HangoverFrames is how many 10ms frames after the last speech frame a band
	// continues to be treated as speech. Prevents word-end clipping.
	// Default 12 (120ms). Range 6–20.
	HangoverFrames int

	// GateAttenuation is the gain applied to frames classified as pure-noise
	// after Wiener processing (second-stage soft gate). Default 0.08.
	GateAttenuation float64

	// aggressiveness is the atomic aggressiveness level (0/1=mild, 2=medium, 3=aggressive).
	aggressiveness int32

	// NoiseEMACoeff is the EMA coefficient for the per-band noise floor tracker.
	// Higher = slower adaptation (better for stationary noise).
	// Default 0.997.
	NoiseEMACoeff float64
}

const (
	nrBands    = 8
	nrBandSize = FrameSizeSamples / nrBands // 20 samples per band @ 16kHz/160-sample frame
)

// NewAdaptiveNoiseReducer returns an AdaptiveNoiseReducer tuned for telephony
// with HVAC/office-fan background noise (matches raw_audio.wav profile).
// All parameters are exported and can be adjusted after construction.
func NewAdaptiveNoiseReducer() *AdaptiveNoiseReducer {
	r := &AdaptiveNoiseReducer{
		AlphaG:          0.96,
		AlphaP:          0.94,
		OversubFactor:   0.85,
		SpeechThresh:    280,
		MinGainSpeech:   0.55,
		MinGainNoise:    0.08,
		HangoverFrames:  12,
		GateAttenuation: 0.08,
		NoiseEMACoeff:   0.997,
	}
	// Initialise per-band gain to 1.0 (pass-through) — avoids a silent
	// transient on the very first frame.
	for b := range r.bandGainPrev {
		r.bandGainPrev[b] = 1.0
		r.bandSNRPrev[b] = 1.0
	}
	return r
}

// SetAggressiveness adjusts suppression strength without restarting the session.
// n=0 or 1: mild — AlphaG=0.97, MinGainSpeech=0.65, GateAttenuation=0.12
// n=2:      medium — AlphaG=0.96, MinGainSpeech=0.55, GateAttenuation=0.08 (default)
// n=3:      aggressive — AlphaG=0.94, MinGainSpeech=0.40, GateAttenuation=0.04
// Thread-safe: uses atomic store.
func (r *AdaptiveNoiseReducer) SetAggressiveness(n int) {
	atomic.StoreInt32(&r.aggressiveness, int32(n))
}

// Name returns the processor identifier.
func (r *AdaptiveNoiseReducer) Name() string { return "adaptive-nr-dd" }

// Process applies decision-directed Wiener noise reduction to frame and
// returns the cleaned samples. len(out) == len(frame) always.
func (r *AdaptiveNoiseReducer) Process(frame []int16) ([]int16, error) {
	if len(frame) == 0 {
		return frame, nil
	}

	// Read aggressiveness level atomically and derive local tuning vars.
	ag := atomic.LoadInt32(&r.aggressiveness)
	alphaG := r.AlphaG
	minGainSpeech := r.MinGainSpeech
	gateAtten := r.GateAttenuation
	switch ag {
	case 0, 1:
		alphaG = 0.97
		minGainSpeech = 0.65
		gateAtten = 0.12
	case 3:
		alphaG = 0.94
		minGainSpeech = 0.40
		gateAtten = 0.04
	}

	// Convert to float64 for processing.
	fIn := make([]float64, len(frame))
	for i, s := range frame {
		fIn[i] = float64(s)
	}

	nBands := len(frame) / nrBandSize
	if nBands > nrBands {
		nBands = nrBands
	}

	// Track whether any band is in speech this frame (for hangover).
	speechThisFrame := false

	bandGain := [nrBands]float64{}

	for b := 0; b < nBands; b++ {
		start := b * nrBandSize
		end := start + nrBandSize

		// --- Band RMS ---
		var sumSq float64
		for _, v := range fIn[start:end] {
			sumSq += v * v
		}
		rms := math.Sqrt(sumSq / float64(nrBandSize))

		// --- Noise floor EMA (update only on non-speech frames) ---
		floor := r.bandNoiseFloor[b]
		isSpeech := rms >= r.SpeechThresh
		if isSpeech {
			speechThisFrame = true
		}
		if !isSpeech {
			// Noise / silence frame: floor tracks toward rms.
			floor = floor*r.NoiseEMACoeff + rms*(1-r.NoiseEMACoeff)
			r.bandNoiseFloor[b] = floor
		}
		// During speech frames the floor is frozen — a background voice will not
		// corrupt the noise floor estimate.

		if floor < 1 {
			floor = 1 // avoid divide-by-zero
		}

		// --- A posteriori SNR (γ) ---
		postSNR := (rms/floor)*(rms/floor) - 1
		if postSNR < 0 {
			postSNR = 0
		}

		// --- Decision-directed a priori SNR (Ephraim-Malah) ---
		prevGain := r.bandGainPrev[b]
		prevSNR := r.bandSNRPrev[b]
		aprioriSNR := r.AlphaP*(prevGain*prevGain)*prevSNR + (1-r.AlphaP)*postSNR
		if aprioriSNR < 0 {
			aprioriSNR = 0
		}
		r.bandSNRPrev[b] = aprioriSNR

		// --- Wiener gain ---
		rawGain := aprioriSNR / (aprioriSNR + r.OversubFactor)

		// --- Temporal gain smoothing (eliminates musical noise) ---
		smoothedGain := alphaG*prevGain + (1-alphaG)*rawGain

		// --- Class-dependent gain floor ---
		// Determine effective speech status: actual speech OR within hangover.
		effectiveSpeech := isSpeech || r.hangoverCount > 0
		minGain := r.MinGainNoise
		if effectiveSpeech {
			minGain = minGainSpeech
		}
		if smoothedGain < minGain {
			smoothedGain = minGain
		}
		if smoothedGain > 1.0 {
			smoothedGain = 1.0
		}

		bandGain[b] = smoothedGain
		r.bandGainPrev[b] = smoothedGain
	}

	// --- Update hangover counter ---
	if speechThisFrame {
		r.hangoverCount = r.HangoverFrames
	} else if r.hangoverCount > 0 {
		r.hangoverCount--
	}

	// --- Apply per-band gain ---
	fOut := make([]float64, len(fIn))
	copy(fOut, fIn) // pass-through remainder beyond processed bands
	for b := 0; b < nBands; b++ {
		start := b * nrBandSize
		end := start + nrBandSize
		g := bandGain[b]
		for i := start; i < end; i++ {
			fOut[i] = fIn[i] * g
		}
	}

	// --- Global noise floor tracking (for soft gate) ---
	var sumSqAll float64
	for _, v := range fOut {
		sumSqAll += v * v
	}
	frameRMS := math.Sqrt(sumSqAll / float64(len(fOut)))

	r.frameCount++
	if r.frameCount == 1 {
		r.globalNoiseEMA = frameRMS
	} else {
		if frameRMS < r.globalNoiseEMA {
			r.globalNoiseEMA = r.globalNoiseEMA*0.990 + frameRMS*0.010
		} else {
			r.globalNoiseEMA = r.globalNoiseEMA*0.9995 + frameRMS*0.0005
		}
	}

	// --- Soft gate (second-stage) ---
	// Pure-noise frames that slipped through the Wiener stage are attenuated
	// to GateAttenuation. Guard: only apply after the first 50 frames so the
	// noise floor EMA has time to stabilise.
	if r.frameCount > 50 {
		gateThresh := r.globalNoiseEMA * 1.5
		if frameRMS < gateThresh && r.hangoverCount == 0 {
			for i := range fOut {
				fOut[i] *= gateAtten
			}
		}
	}

	// --- Convert back to int16 with hard clipping ---
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

// Reset clears all internal state. Call this between independent audio streams
// so stale noise floor estimates do not bleed across calls.
func (r *AdaptiveNoiseReducer) Reset() {
	for b := range r.bandNoiseFloor {
		r.bandNoiseFloor[b] = 0
		r.bandGainPrev[b] = 1.0 // pass-through on first frame after reset
		r.bandSNRPrev[b] = 1.0
	}
	r.globalNoiseEMA = 0
	r.frameCount = 0
	r.hangoverCount = 0
}
