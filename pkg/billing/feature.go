package billing

import "strings"

// Feature is a bitmask of active ClearStream features in a session.
type Feature uint8

const (
	FeatureVAD        Feature = 1 << 0 // 0x01 — Voice Activity Detection
	FeatureSpectralNR Feature = 1 << 1 // 0x02 — Adaptive spectral noise reduction
	FeatureRNNoise    Feature = 1 << 2 // 0x04 — RNNoise ML suppressor
	FeatureDeepFilter Feature = 1 << 3 // 0x08 — DeepFilterNet suppressor
	FeatureAGC        Feature = 1 << 4 // 0x10 — Automatic Gain Control
	FeatureRTPMonitor Feature = 1 << 5 // 0x20 — Real-time RTP quality monitor
	FeatureEval       Feature = 1 << 6 // 0x40 — Post-call eval & config tuning
)

// Has reports whether all bits in flag are set in f.
func (f Feature) Has(flag Feature) bool { return f&flag != 0 }

// String returns a comma-separated list of active feature names.
func (f Feature) String() string {
	type entry struct {
		bit  Feature
		name string
	}
	all := []entry{
		{FeatureVAD, "VAD"},
		{FeatureSpectralNR, "SpectralNR"},
		{FeatureRNNoise, "RNNoise"},
		{FeatureDeepFilter, "DeepFilter"},
		{FeatureAGC, "AGC"},
		{FeatureRTPMonitor, "RTPMonitor"},
		{FeatureEval, "Eval"},
	}
	var parts []string
	for _, e := range all {
		if f.Has(e.bit) {
			parts = append(parts, e.name)
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}
