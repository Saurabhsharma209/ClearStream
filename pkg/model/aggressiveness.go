package model

// This file implements SuppressorConfig.Aggressiveness for backends whose
// underlying models (RNNoise, RNNoise-ONNX, DeepFilterNet) do not expose a
// native suppression-strength knob. Previously Aggressiveness was documented
// ("RNNoise and future backends use it to tune their internal parameters"),
// set by every NoiseProfile in profile.go, and even asserted on in
// profile_test.go -- but NewSuppressor never passed it to any backend
// constructor, and no backend constructor accepted or used it. The field was
// pure dead wiring: every profile's chosen aggressiveness silently had zero
// effect on the audio.
//
// The fix implements aggressiveness as a wet/dry blend between the model's
// output (fully suppressed) and the original frame: lower aggressiveness
// blends in more of the original signal (less noise removed, fewer
// artifacts), while level 3 and the zero value (backend default) return the
// model's full-strength output unchanged, preserving prior behavior for
// every caller that never set the field.
//
// This file has no build tags so the blending logic itself is covered by
// pkg/model's default (CGO_ENABLED=0, no build tags) test run, independent
// of whether the CGo "rnnoise" or "onnx" backends are compiled in.

// aggressivenessWetRatio maps a SuppressorConfig.Aggressiveness level to the
// fraction of the model's ("wet") output to keep, with the remainder made up
// from the original ("dry") frame.
//
//   - 0 (unset / "backend default") and 3 ("aggressive"): 1.0 -- full model
//     strength, unchanged from pre-fix behavior.
//   - 1 ("mild"): 0.40 -- mostly original signal, light suppression.
//   - 2 ("medium"): 0.70 -- mostly suppressed, some original blended back in.
//   - anything else (out-of-range/negative): 1.0, matching the documented
//     "0=backend default" fallback rather than silently misbehaving.
func aggressivenessWetRatio(level int) float64 {
	switch level {
	case 1:
		return 0.40
	case 2:
		return 0.70
	default:
		return 1.0
	}
}

// blendAggressiveness mixes original (pre-suppression) and processed
// (post-suppression) frames according to level, per aggressivenessWetRatio.
// If the wet ratio is 1.0, or the two frames differ in length (should not
// happen for well-behaved backends, but blending would panic on index
// mismatch), processed is returned unchanged.
func blendAggressiveness(original, processed []int16, level int) []int16 {
	wet := aggressivenessWetRatio(level)
	if wet >= 1.0 || len(original) != len(processed) {
		return processed
	}
	dry := 1.0 - wet
	out := make([]int16, len(processed))
	for i := range processed {
		v := float64(processed[i])*wet + float64(original[i])*dry
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}
