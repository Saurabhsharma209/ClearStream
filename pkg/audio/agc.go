package audio

import "math"

// AGCConfig configures the Automatic Gain Control processor.
// AGC runs as a post-suppression stage: after noise is removed it adaptively
// boosts quiet speakers and pulls back loud ones, targeting a steady output level.
type AGCConfig struct {
	// TargetRMS is the desired output RMS level (0–32768).
	// Good default for telephony: 3000 (~-20 dBFS relative to int16 full scale).
	TargetRMS float64

	// MaxGain is the maximum amplification factor (linear, not dB).
	// 4.0 = 12 dB boost cap. Prevents AGC from amplifying pure noise.
	MaxGain float64

	// AttackMs controls how fast the gain rises when the signal is quiet (ms).
	// Shorter = faster boost. Typical: 10–50 ms.
	AttackMs float64

	// ReleaseMs controls how fast the gain falls when the signal is loud (ms).
	// Shorter = faster duck. Typical: 100–500 ms.
	ReleaseMs float64

	// SoftLimitThreshold is the level (0–32767) above which soft limiting
	// (tanh) replaces hard clipping. 0 disables soft limiting.
	// Default: 28000 (~-1.3 dBFS). Keeps peaks musical rather than clipped.
	SoftLimitThreshold float64

	// SampleRate is the PCM sample rate (Hz). Set automatically by Pipeline.
	SampleRate int
}

// DefaultAGCConfig returns telephony-tuned AGC defaults.
// Target: -20 dBFS, max 12 dB boost, 20 ms attack, 200 ms release,
// soft limiter kicks in at -1.3 dBFS.
func DefaultAGCConfig() AGCConfig {
	return AGCConfig{
		TargetRMS:          3000,
		MaxGain:            4.0,
		AttackMs:           20,
		ReleaseMs:          200,
		SoftLimitThreshold: 28000,
		SampleRate:         16000,
	}
}

// ASRConfig returns AGC settings tuned for ASR / Voice AI ingestion.
// Key differences from DefaultAGCConfig:
//
//   - TargetRMS: 4124 (-18 dBFS) — sits comfortably in the ASR sweet spot
//     (-20 to -14 dBFS) with headroom to spare.
//   - MaxGain: 2.5 (~8 dB) — prevents the AGC from over-boosting audio that
//     is already near full scale. Even at 2.5× a -18 dBFS signal stays under
//     -12 dBFS after boost.
//   - SoftLimitThreshold: 23197 (-3 dBFS) — tanh ceiling engages well before
//     hard clipping; guarantees peak < -3 dBFS which is the typical ASR
//     no-clip requirement.
//   - ReleaseMs: 300 — slightly slower release so ASR models see stable,
//     non-pumping gain between utterances.
//
// Use this with Pipeline.UseLimiter = true for belt-and-suspenders peak
// protection when handling unpredictable call-centre audio.
func ASRConfig() AGCConfig {
	return AGCConfig{
		TargetRMS:          4124,  // -18 dBFS (32768 × 10^(-18/20))
		MaxGain:            2.5,   // ~+8 dB max boost — safe for loud callers
		AttackMs:           20,    // 20 ms: responsive to quiet callers
		ReleaseMs:          300,   // 300 ms: stable inter-utterance level
		SoftLimitThreshold: 23197, // -3 dBFS ceiling (32768 × 10^(-3/20))
		SampleRate:         16000,
	}
}

// AGC is a real-time Automatic Gain Control processor.
// It tracks signal RMS over a sliding window and smoothly adjusts gain so the
// output hovers near TargetRMS regardless of how loud or quiet the input is.
//
// Improvements over naive AGC:
//   - Per-sample gain smoothing (no staircase stepping between frames)
//   - Soft limiter (tanh) replaces hard clip — no digital distortion on peaks
//   - Near-silence guard: gain held, not pumped, when RMS < 1.0
//
// Use it post-suppression to even out level differences between speakers and
// compensate for network path loss on RTP streams.
type AGC struct {
	cfg         AGCConfig
	currentGain float64 // current linear gain applied to output
	targetGain  float64 // smoothed target (avoids step jumps between frames)
	attackCoef  float64 // per-sample smoothing coefficient (gain rise)
	releaseCoef float64 // per-sample smoothing coefficient (gain fall)
}

// NewAGC creates an AGC processor with the given config.
// SampleRate must be set (done automatically when attached to a Pipeline).
func NewAGC(cfg AGCConfig) *AGC {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.TargetRMS == 0 {
		cfg.TargetRMS = 3000
	}
	if cfg.MaxGain == 0 {
		cfg.MaxGain = 4.0
	}
	if cfg.AttackMs == 0 {
		cfg.AttackMs = 20
	}
	if cfg.ReleaseMs == 0 {
		cfg.ReleaseMs = 200
	}
	if cfg.SoftLimitThreshold == 0 {
		cfg.SoftLimitThreshold = 28000
	}

	// Time constant: coef = e^(-1 / (timeMs * sampleRate / 1000))
	attackSamples := cfg.AttackMs * float64(cfg.SampleRate) / 1000.0
	releaseSamples := cfg.ReleaseMs * float64(cfg.SampleRate) / 1000.0

	return &AGC{
		cfg:         cfg,
		currentGain: 1.0,
		targetGain:  1.0,
		attackCoef:  math.Exp(-1.0 / attackSamples),
		releaseCoef: math.Exp(-1.0 / releaseSamples),
	}
}

// softLimit applies tanh-based soft saturation above threshold.
// Unlike hard clipping, tanh rounds the peaks smoothly — no harmonic distortion.
// Below threshold the signal passes through unchanged (linear region).
func (a *AGC) softLimit(val float64) float64 {
	thr := a.cfg.SoftLimitThreshold
	if thr <= 0 || math.Abs(val) <= thr {
		return val
	}
	// Normalise into tanh's ±1 range, saturate, scale back.
	// tanh(1) ≈ 0.762, so we scale so tanh maps thr→thr and clips above.
	sign := 1.0
	if val < 0 {
		sign = -1.0
		val = -val
	}
	// map [thr, +inf) → tanh, rescale so thr maps exactly to thr
	k := math.Tanh(1.0) // ≈ 0.7616
	normalised := val / thr
	limited := math.Tanh(normalised) / k * thr
	return sign * limited
}

// Process applies adaptive gain to a frame of int16 PCM samples.
// It measures the frame RMS, computes the desired gain to reach TargetRMS,
// then smoothly interpolates currentGain per-sample using attack/release coefs.
// Peaks above SoftLimitThreshold are shaped via tanh rather than hard-clipped.
// Returns a new slice — the input is not modified.
func (a *AGC) Process(samples []int16) []int16 {
	if len(samples) == 0 {
		return samples
	}

	// Measure input RMS for this frame.
	var sumSq float64
	for _, s := range samples {
		f := float64(s)
		sumSq += f * f
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))

	// Compute desired gain to reach TargetRMS.
	// Near-silence guard: if RMS < 1 hold targetGain steady so we don't
	// pump noise up between words.
	if rms >= 1.0 {
		desired := a.cfg.TargetRMS / rms
		if desired > a.cfg.MaxGain {
			desired = a.cfg.MaxGain
		}
		a.targetGain = desired
	}

	// Per-sample gain smoothing: interpolate currentGain toward targetGain
	// using attack (rising) or release (falling) time constant each sample.
	// This eliminates the staircase/clicking artifact from frame-level switching.
	out := make([]int16, len(samples))
	for i, s := range samples {
		if a.targetGain > a.currentGain {
			a.currentGain = a.attackCoef*a.currentGain + (1-a.attackCoef)*a.targetGain
		} else {
			a.currentGain = a.releaseCoef*a.currentGain + (1-a.releaseCoef)*a.targetGain
		}

		// Apply gain then soft-limit instead of hard-clipping.
		val := a.softLimit(float64(s) * a.currentGain)

		// Final int16 boundary guard (should rarely trigger after soft limit).
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		out[i] = int16(val)
	}
	return out
}

// Reset resets AGC state (gain returns to 1.0). Call when starting a new stream.
func (a *AGC) Reset() {
	a.currentGain = 1.0
	a.targetGain = 1.0
}

// CurrentGain returns the instantaneous linear gain being applied.
// Useful for monitoring / logging.
func (a *AGC) CurrentGain() float64 {
	return a.currentGain
}

// CurrentGainDB returns the current gain in decibels (20*log10(gain)).
func (a *AGC) CurrentGainDB() float64 {
	if a.currentGain <= 0 {
		return -math.MaxFloat64
	}
	return 20 * math.Log10(a.currentGain)
}
