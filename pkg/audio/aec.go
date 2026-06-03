package audio

import "math"

// AECConfig configures the Acoustic Echo Canceller.
type AECConfig struct {
	// FilterLen is the adaptive filter length in samples.
	// At 16kHz, 512 covers 32ms of echo tail — enough for headsets.
	// For speakerphone, use 1024–2048 (64–128ms).
	// Default: 512
	FilterLen int

	// StepSize (mu) controls adaptation speed. Range: 0.0–1.0.
	// Larger = faster adaptation but less stable. Telephony default: 0.1
	StepSize float64

	// Leakage is a small forgetting factor (0.999–1.0) to prevent filter drift.
	Leakage float64

	// SampleRate is the audio sample rate (8000 or 16000).
	SampleRate int
}

// DefaultAECConfig returns a config tuned for telephone headsets (16kHz, 32ms echo tail).
func DefaultAECConfig() AECConfig {
	return AECConfig{
		FilterLen:  512,
		StepSize:   0.1,
		Leakage:    0.9999,
		SampleRate: 16000,
	}
}

// NarrowbandAECConfig returns a config tuned for Indian PSTN (8kHz).
// Shorter filter is sufficient for narrowband telephone earpieces.
func NarrowbandAECConfig() AECConfig {
	return AECConfig{
		FilterLen:  256, // 32ms at 8kHz
		StepSize:   0.1,
		Leakage:    0.9999,
		SampleRate: 8000,
	}
}

// AEC is an Acoustic Echo Canceller using an NLMS adaptive filter.
// Feed the far-end (reference) signal and the near-end microphone signal;
// it outputs the near-end signal with echo removed.
//
// Usage:
//
//	aec := NewAEC(DefaultAECConfig())
//	cleaned := aec.Process(farEnd, nearEnd)
type AEC struct {
	cfg    AECConfig
	w      []float64 // adaptive filter coefficients
	refBuf []float64 // circular reference buffer (far-end history)
	pos    int       // write position in circular buffer
}

// NewAEC creates a new AEC with the given config.
func NewAEC(cfg AECConfig) *AEC {
	if cfg.FilterLen <= 0 {
		cfg.FilterLen = 512
	}
	if cfg.StepSize <= 0 {
		cfg.StepSize = 0.1
	}
	if cfg.Leakage <= 0 {
		cfg.Leakage = 0.9999
	}
	return &AEC{
		cfg:    cfg,
		w:      make([]float64, cfg.FilterLen),
		refBuf: make([]float64, cfg.FilterLen),
	}
}

// Process cancels echo from nearEnd using farEnd as the reference signal.
// Both slices must be the same length. Returns cleaned near-end samples.
// If farEnd is nil or empty, returns nearEnd unchanged (bypass mode for half-duplex).
func (a *AEC) Process(farEnd, nearEnd []int16) []int16 {
	if len(farEnd) == 0 || len(nearEnd) == 0 {
		return nearEnd
	}
	out := make([]int16, len(nearEnd))
	for i := range nearEnd {
		// Insert far-end sample into circular buffer
		x := float64(farEnd[i]) / 32768.0
		a.refBuf[a.pos] = x
		a.pos = (a.pos + 1) % len(a.refBuf)

		// Compute filter output (estimated echo): y = w^T * x_vec
		y := 0.0
		for k := 0; k < a.cfg.FilterLen; k++ {
			idx := (a.pos - 1 - k + len(a.refBuf)) % len(a.refBuf)
			y += a.w[k] * a.refBuf[idx]
		}

		// Error signal: near-end minus estimated echo
		d := float64(nearEnd[i]) / 32768.0
		e := d - y

		// NLMS update: w = leakage*w + (mu * e / (||x||^2 + eps)) * x_vec
		xPowerSq := 0.0
		for k := 0; k < a.cfg.FilterLen; k++ {
			idx := (a.pos - 1 - k + len(a.refBuf)) % len(a.refBuf)
			xPowerSq += a.refBuf[idx] * a.refBuf[idx]
		}
		mu := a.cfg.StepSize / (xPowerSq + 1e-6)
		for k := 0; k < a.cfg.FilterLen; k++ {
			idx := (a.pos - 1 - k + len(a.refBuf)) % len(a.refBuf)
			a.w[k] = a.cfg.Leakage*a.w[k] + mu*e*a.refBuf[idx]
		}

		// Clip to int16 range
		sample := math.Round(e * 32768.0)
		if sample > 32767 {
			sample = 32767
		}
		if sample < -32768 {
			sample = -32768
		}
		out[i] = int16(sample)
	}
	return out
}

// Reset clears the adaptive filter state (call on new call leg).
func (a *AEC) Reset() {
	for i := range a.w {
		a.w[i] = 0
	}
	for i := range a.refBuf {
		a.refBuf[i] = 0
	}
	a.pos = 0
}

// FilterLen returns the configured filter length.
func (a *AEC) FilterLen() int { return a.cfg.FilterLen }
