package audio

import (
	"math"
	"sync"

	"github.com/exotel/clearstream/pkg/model"
)

// TieredNRConfig configures the three-tier noise reduction ladder.
// Each tier is selected based on the estimated per-frame SNR.
type TieredNRConfig struct {
	// HighSNRThreshold: frames above this use gate only (default 25 dB)
	HighSNRThreshold float64
	// LowSNRThreshold: frames below this use DeepFilter (default 10 dB)
	LowSNRThreshold float64
	// RNNoise is the mid-tier suppressor (may be nil → gate used for mid tier too)
	RNNoise model.Suppressor
	// DeepFilter is the high-quality low-SNR suppressor (may be nil → gate fallback)
	DeepFilter model.Suppressor
}

// DefaultTieredNRConfig returns telephony-tuned defaults.
func DefaultTieredNRConfig() TieredNRConfig {
	return TieredNRConfig{
		HighSNRThreshold: 25.0,
		LowSNRThreshold:  10.0,
	}
}

// TieredNR selects the noise reduction backend per-frame based on estimated SNR:
//
//	SNR > HighSNRThreshold  → Ephraim-Malah spectral gate only     (~0.1 ms/frame)
//	LowSNRThreshold ≤ SNR ≤ HighSNRThreshold → gate + RNNoise      (~0.6 ms/frame)
//	SNR < LowSNRThreshold   → DeepFilterNet                        (~3 ms/frame)
//
// If RNNoise or DeepFilter are nil, the gate is used as fallback for that tier.
type TieredNR struct {
	mu         sync.Mutex
	gate       *AdaptiveNoiseReducer
	rnnoise    model.Suppressor // may be nil
	deepfilter model.Suppressor // may be nil
	cfg        TieredNRConfig
	// noise floor EMA for SNR estimation
	noiseFloor float64
	emaCoeff   float64 // 0.995
}

// NewTieredNR creates a TieredNR with the given config.
func NewTieredNR(cfg TieredNRConfig) *TieredNR {
	return &TieredNR{
		gate:       NewAdaptiveNoiseReducer(),
		rnnoise:    cfg.RNNoise,
		deepfilter: cfg.DeepFilter,
		cfg:        cfg,
		noiseFloor: 1.0,
		emaCoeff:   0.995,
	}
}

// Process selects the appropriate suppressor tier based on estimated frame SNR.
func (t *TieredNR) Process(frame []int16) ([]int16, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	snr := t.estimateSNR(frame)

	switch {
	case snr > t.cfg.HighSNRThreshold:
		// High SNR: gate only
		out, err := t.gate.Process(frame)
		return out, err

	case snr < t.cfg.LowSNRThreshold:
		// Low SNR: DeepFilter (or gate fallback)
		if t.deepfilter != nil {
			return t.deepfilter.Process(frame)
		}
		out, err := t.gate.Process(frame)
		return out, err

	default:
		// Mid SNR: gate + RNNoise (or gate fallback)
		gated, err := t.gate.Process(frame)
		if err != nil || t.rnnoise == nil {
			return gated, err
		}
		return t.rnnoise.Process(gated)
	}
}

// Reset clears internal state on all tiers.
func (t *TieredNR) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gate.Reset()
	if t.rnnoise != nil {
		t.rnnoise.Reset()
	}
	if t.deepfilter != nil {
		t.deepfilter.Reset()
	}
	t.noiseFloor = 1.0
}

// estimateSNR returns a blind SNR estimate in dB using RMS vs noise floor EMA.
// Must be called with t.mu held.
func (t *TieredNR) estimateSNR(frame []int16) float64 {
	var sum float64
	for _, s := range frame {
		f := float64(s)
		sum += f * f
	}
	rms := 1.0
	if len(frame) > 0 {
		rms = math.Sqrt(sum / float64(len(frame)))
		if rms < 1 {
			rms = 1
		}
	}
	// Update noise floor EMA only on quiet frames
	if rms < t.noiseFloor*3 {
		t.noiseFloor = t.emaCoeff*t.noiseFloor + (1-t.emaCoeff)*rms
	}
	if t.noiseFloor < 1 {
		t.noiseFloor = 1
	}
	return 20 * math.Log10(rms/t.noiseFloor)
}
