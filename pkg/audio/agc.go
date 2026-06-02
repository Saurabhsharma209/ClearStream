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

	// SampleRate is the PCM sample rate (Hz). Set automatically by Pipeline.
	SampleRate int
}

// DefaultAGCConfig returns telephony-tuned AGC defaults.
// Target: -20 dBFS, max 12 dB boost, 20 ms attack, 200 ms release.
func DefaultAGCConfig() AGCConfig {
	return AGCConfig{
		TargetRMS:  3000,
		MaxGain:    4.0,
		AttackMs:   20,
		ReleaseMs:  200,
		SampleRate: 16000,
	}
}

// AGC is a real-time Automatic Gain Control processor.
// It tracks signal RMS over a sliding window and smoothly adjusts gain so the
// output hovers near TargetRMS regardless of how loud or quiet the input is.
//
// Use it post-suppression to even out level differences between speakers and
// compensate for network path loss on RTP streams.
type AGC struct {
	cfg        AGCConfig
	currentGain float64 // current linear gain applied to output
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

	// Time constant: coef = e^(-1 / (timeMs * sampleRate / 1000))
	attackSamples := cfg.AttackMs * float64(cfg.SampleRate) / 1000.0
	releaseSamples := cfg.ReleaseMs * float64(cfg.SampleRate) / 1000.0

	return &AGC{
		cfg:         cfg,
		currentGain: 1.0,
		attackCoef:  math.Exp(-1.0 / attackSamples),
		releaseCoef: math.Exp(-1.0 / releaseSamples),
	}
}

// Process applies adaptive gain to a frame of int16 PCM samples.
// It measures the frame RMS, computes the desired gain to reach TargetRMS,
// then smoothly interpolates currentGain using attack/release coefficients.
// Returns a new slice — the input is not modified.
func (a *AGC) Process(samples []int16) []int16 {
	if len(samples) == 0 {
		return samples
	}

	// Measure input RMS
	var sumSq float64
	for _, s := range samples {
		f := float64(s)
		sumSq += f * f
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))

	// Desired gain: how much do we need to reach TargetRMS?
	var desiredGain float64
	if rms < 1.0 {
		// Near-silence: hold current gain (don't amplify pure noise)
		desiredGain = a.currentGain
	} else {
		desiredGain = a.cfg.TargetRMS / rms
		if desiredGain > a.cfg.MaxGain {
			desiredGain = a.cfg.MaxGain
		}
	}

	// Apply per-sample gain with attack/release smoothing
	out := make([]int16, len(samples))
	for i, s := range samples {
		if desiredGain > a.currentGain {
			// Gain rising: use attack (slow ramp up to avoid clicks)
			a.currentGain = a.attackCoef*a.currentGain + (1-a.attackCoef)*desiredGain
		} else {
			// Gain falling: use release (fast duck on loud signals)
			a.currentGain = a.releaseCoef*a.currentGain + (1-a.releaseCoef)*desiredGain
		}

		// Apply gain and hard-clip to int16 range
		val := float64(s) * a.currentGain
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
