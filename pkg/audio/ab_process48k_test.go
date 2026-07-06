package audio

import (
	"bytes"
	"math"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

// ---------------------------------------------------------------------------
// A/B test: Process48k (48kHz native round-trip) vs the direct 8kHz path
// (upsample 8k->16k, suppress, downsample 16k->8k) on realistic, synthetic
// call audio. This quantifies the SNR delta between the two resampling
// strategies referenced by the "Process48k vs 8kHz path" priority, using the
// already-established SNREstimator from quality.go and the sine+noise
// fixture pattern already used across this package (see quality_test.go's
// makeSineFrame/addNoise and testdata/generate_noisy.go).
// ---------------------------------------------------------------------------

// nrSuppressor adapts AdaptiveNoiseReducer (a real, non-mock spectral Wiener
// noise reducer already implemented in this package) to the model.Suppressor
// interface so the exact same denoising algorithm can be plugged into both
// the Process48k path and the direct-8kHz ProcessFrames path for a fair,
// like-for-like A/B comparison. AdaptiveNoiseReducer already exposes
// Process/Reset/Name; only Close is added here to complete the interface.
type nrSuppressor struct {
	*AdaptiveNoiseReducer
}

func (nrSuppressor) Close() error { return nil }

func newNRSuppressor() *nrSuppressor {
	return &nrSuppressor{NewAdaptiveNoiseReducer()}
}

var _ model.Suppressor = (*nrSuppressor)(nil)

// synthCallSignal generates a deterministic, speech-like PCM signal at the
// given sample rate: a voiced pitch fundamental (~120Hz, typical male voice)
// plus decaying harmonics, shaped by a slow syllabic-rate amplitude envelope.
// This mirrors the tone-based synthetic fixtures already used elsewhere in
// this package (quality_test.go's makeSineFrame, testdata/generate_noisy.go's
// 440Hz sine fixtures) but adds harmonics + an envelope to better approximate
// real call audio instead of a single pure tone.
func synthCallSignal(sampleRate, numSamples int) []int16 {
	const (
		f0    = 120.0  // fundamental pitch (Hz)
		envHz = 3.5    // syllabic-rate amplitude modulation (Hz)
		amp   = 9000.0 // base amplitude
	)
	harmonics := []struct{ mult, amp float64 }{
		{1, 1.00},
		{2, 0.55},
		{3, 0.30},
		{5, 0.15},
	}
	var harmSum float64
	for _, h := range harmonics {
		harmSum += h.amp
	}

	out := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		// Envelope stays in [0.55, 1.0] so the signal is "always voiced"
		// (no true silence gaps) which keeps the AB comparison focused on
		// suppression/resampling quality rather than VAD behaviour.
		env := 0.55 + 0.45*math.Abs(math.Sin(2*math.Pi*envHz*t))
		var v float64
		for _, h := range harmonics {
			v += h.amp * math.Sin(2*math.Pi*f0*h.mult*t)
		}
		v = v / harmSum * amp * env
		out[i] = clampInt16(v)
	}
	return out
}

// addWhiteNoiseAt adds deterministic white noise to a clean signal at
// approximately the given target SNR (dB), using the same xorshift32
// deterministic generator already used by quality_test.go's addNoise helper
// (kept reproducible across test runs without importing math/rand).
func addWhiteNoiseAt(clean []int16, targetSNRdB float64, seed uint32) []int16 {
	sigRMS := rmsF(clean)
	// RMS of uniform noise in [-a,a] is a/sqrt(3); solve for `a` given the
	// target SNR expressed as 20*log10(sigRMS/noiseRMS).
	noiseRMS := sigRMS / math.Pow(10, targetSNRdB/20.0)
	noiseAmp := noiseRMS * math.Sqrt(3)

	out := make([]int16, len(clean))
	state := seed
	for i, s := range clean {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		noise := (float64(int32(state)>>16) / 32768.0) * noiseAmp
		v := float64(s) + noise
		out[i] = clampInt16(v)
	}
	return out
}

func clampInt16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// snrVsCleanReference computes real signal-to-noise ratio in dB between a
// processed signal and its known-clean reference, using the already-defined
// SNREstimator.EstimateSNR from quality.go. The residual (reference minus
// processed) is treated as the "noise" term, which is the standard
// definition of SNR relative to ground truth.
func snrVsCleanReference(estimator *SNREstimator, processed, reference []int16) float64 {
	n := len(processed)
	if len(reference) < n {
		n = len(reference)
	}
	residual := make([]int16, n)
	for i := 0; i < n; i++ {
		d := int32(reference[i]) - int32(processed[i])
		if d > 32767 {
			d = 32767
		} else if d < -32768 {
			d = -32768
		}
		residual[i] = int16(d)
	}
	return estimator.EstimateSNR(reference[:n], residual)
}

// runDirect8kPath feeds noisy 8kHz PCM through Pipeline.ProcessFrames with
// InputSampleRate=8000 (the native 8kHz processing path: upsample 8k->16k
// via the Kaiser FIR filter in resample.go, suppress at 16kHz, downsample
// 16k->8k via the Kaiser FIR filter) and returns the resulting 8kHz PCM.
func runDirect8kPath(t *testing.T, noisy8k []int16) []int16 {
	t.Helper()
	p := NewPipeline(PipelineConfig{
		SampleRate:      8000,
		Channels:        1,
		InputSampleRate: 8000,
		Suppressor:      newNRSuppressor(),
	})

	in := int16ToBytes(noisy8k)
	var out bytes.Buffer
	if err := p.ProcessFrames(in, &out); err != nil {
		t.Fatalf("direct 8kHz path: ProcessFrames error: %v", err)
	}
	if err := p.Flush(&out); err != nil {
		t.Fatalf("direct 8kHz path: Flush error: %v", err)
	}
	return bytesToInt16(out.Bytes())
}

// runProcess48kPath feeds noisy 48kHz PCM through Pipeline.Process48k frame
// by frame (the alternate path: downsample 48k->16k via 3-sample averaging,
// suppress at 16kHz, upsample 16k->48k via linear interpolation) and returns
// the resulting 48kHz PCM.
func runProcess48kPath(t *testing.T, noisy48k []int16) []int16 {
	t.Helper()
	p := NewPipeline(PipelineConfig{
		Suppressor: newNRSuppressor(),
	})

	out := make([]int16, 0, len(noisy48k))
	for offset := 0; offset+Frame48kSamples <= len(noisy48k); offset += Frame48kSamples {
		frame := noisy48k[offset : offset+Frame48kSamples]
		cleaned, err := p.Process48k(frame)
		if err != nil {
			t.Fatalf("Process48k path: unexpected error: %v", err)
		}
		out = append(out, cleaned...)
	}
	return out
}

// TestABProcess48kVsDirect is the permanent regression test for the
// "A/B test Process48k vs 8kHz path on real call samples to quantify SNR
// improvement" priority. It builds a synthetic-but-realistic noisy call
// signal (voiced harmonic tone + syllabic envelope + white noise, ~10dB
// input SNR — matching the conventions in testdata/generate_noisy.go and
// quality_test.go), runs it through both paths using the same real
// AdaptiveNoiseReducer suppressor, and reports/asserts the SNR each path
// achieves relative to the known-clean reference.
func TestABProcess48kVsDirect(t *testing.T) {
	const (
		durationSeconds = 3
		rate48k         = 48000
		rate8k          = 8000
		inputSNRdB      = 10.0
	)
	numSamples48k := durationSeconds * rate48k // multiple of Frame48kSamples (480)

	clean48k := synthCallSignal(rate48k, numSamples48k)
	noisy48k := addWhiteNoiseAt(clean48k, inputSNRdB, 0xC0FFEE)

	clean8k, err := Resample(clean48k, rate48k, rate8k)
	if err != nil {
		t.Fatalf("failed to build 8kHz clean reference: %v", err)
	}
	noisy8k, err := Resample(noisy48k, rate48k, rate8k)
	if err != nil {
		t.Fatalf("failed to build 8kHz noisy input: %v", err)
	}

	estimator := &SNREstimator{}

	// Common raw-input baseline: the SNR of the call audio before either
	// path has touched it. This must be measured once, pre-resampling, and
	// shared between both paths — measuring "before" separately on each
	// path's own resampled noisy signal is misleading, because the 8kHz
	// path's Kaiser FIR anti-alias filter (used for the 48k->8k downsample)
	// already discards a lot of the out-of-band white-noise energy before
	// the suppressor ever runs, which would inflate its "before" number and
	// make the two paths' improvement deltas incomparable.
	snrRawInput := snrVsCleanReference(estimator, noisy48k, clean48k)

	// Diagnostic-only: SNR of the 8kHz path's noisy input immediately after
	// its own resampling step, before suppression. Useful to see how much
	// of that path's quality comes from resampling vs. the suppressor.
	snrPostResample8k := snrVsCleanReference(estimator, noisy8k, clean8k)

	// Run both processing paths.
	output8k := runDirect8kPath(t, noisy8k)
	output48k := runProcess48kPath(t, noisy48k)

	if len(output8k) == 0 {
		t.Fatal("direct 8kHz path produced no output")
	}
	if len(output48k) != len(noisy48k) {
		t.Fatalf("Process48k path: want %d output samples, got %d", len(noisy48k), len(output48k))
	}

	snrAfter8k := snrVsCleanReference(estimator, output8k, clean8k)
	snrAfter48k := snrVsCleanReference(estimator, output48k, clean48k)

	improvement8k := snrAfter8k - snrRawInput
	improvement48k := snrAfter48k - snrRawInput
	delta := snrAfter8k - snrAfter48k // positive => direct 8kHz path has higher post-suppression SNR

	t.Logf("AB Process48k vs direct-8kHz — raw input SNR=%.2fdB (target %.1fdB)", snrRawInput, inputSNRdB)
	t.Logf("  Direct 8kHz path:  post-resample(pre-suppress)=%.2fdB  after-suppress=%.2fdB  improvement-vs-raw=%+.2fdB", snrPostResample8k, snrAfter8k, improvement8k)
	t.Logf("  Process48k path:   after-suppress=%.2fdB  improvement-vs-raw=%+.2fdB", snrAfter48k, improvement48k)
	t.Logf("  Delta (direct8k - Process48k) final SNR: %+.2fdB", delta)

	for name, v := range map[string]float64{
		"snrRawInput": snrRawInput, "snrPostResample8k": snrPostResample8k,
		"snrAfter8k": snrAfter8k, "snrAfter48k": snrAfter48k,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("%s is non-finite: %v", name, v)
		}
	}

	// Both paths must actually improve SNR relative to their own noisy input —
	// this is the core regression guard: neither resampling/suppression
	// strategy should regress into a no-op or a quality degradation.
	const minImprovementDB = 1.0
	if improvement8k < minImprovementDB {
		t.Errorf("direct 8kHz path: expected SNR improvement >= %.1fdB, got %+.2fdB", minImprovementDB, improvement8k)
	}
	if improvement48k < minImprovementDB {
		t.Errorf("Process48k path: expected SNR improvement >= %.1fdB, got %+.2fdB", minImprovementDB, improvement48k)
	}

	// Sanity bound on the two paths diverging wildly: Process48k's cruder
	// 3-sample-average/linear-interpolation resampling (vs the direct path's
	// Kaiser-windowed FIR resampling) is expected to trail the direct 8kHz
	// path by some margin, but a >20dB gap would indicate something is
	// broken (e.g. Process48k silently not invoking the suppressor).
	if math.Abs(delta) > 20 {
		t.Errorf("Process48k vs direct-8kHz SNR delta implausibly large: %+.2fdB", delta)
	}
}

// BenchmarkABProcess48kVsDirect measures and compares per-frame processing
// throughput of the two paths under the same real suppressor, so the SNR
// A/B comparison in TestABProcess48kVsDirect is complemented by a
// performance A/B baseline (relevant since Process48k exists specifically to
// avoid a second resampling round-trip on wideband input).
func BenchmarkABProcess48kVsDirect(b *testing.B) {
	const (
		rate48k    = 48000
		rate8k     = 8000
		inputSNRdB = 10.0
	)
	clean48k := synthCallSignal(rate48k, rate48k) // 1 second of fixture, reused per iteration
	noisy48k := addWhiteNoiseAt(clean48k, inputSNRdB, 0xC0FFEE)
	noisy8k, err := Resample(noisy48k, rate48k, rate8k)
	if err != nil {
		b.Fatalf("failed to build 8kHz noisy input: %v", err)
	}
	in8kBytes := int16ToBytes(noisy8k)

	b.Run("Direct8kHz", func(b *testing.B) {
		p := NewPipeline(PipelineConfig{
			SampleRate:      rate8k,
			Channels:        1,
			InputSampleRate: rate8k,
			Suppressor:      newNRSuppressor(),
		})
		var out bytes.Buffer
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out.Reset()
			if err := p.ProcessFrames(in8kBytes, &out); err != nil {
				b.Fatalf("ProcessFrames error: %v", err)
			}
		}
	})

	b.Run("Process48k", func(b *testing.B) {
		p := NewPipeline(PipelineConfig{
			Suppressor: newNRSuppressor(),
		})
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for offset := 0; offset+Frame48kSamples <= len(noisy48k); offset += Frame48kSamples {
				if _, err := p.Process48k(noisy48k[offset : offset+Frame48kSamples]); err != nil {
					b.Fatalf("Process48k error: %v", err)
				}
			}
		}
	})
}
