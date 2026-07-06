package audio_test

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// TestABRunnerDefaultConfig verifies DefaultABConfig returns the documented defaults.
func TestABRunnerDefaultConfig(t *testing.T) {
	cfg := audio.DefaultABConfig()
	if cfg.SilenceThresh != 50 {
		t.Errorf("SilenceThresh: want 50, got %v", cfg.SilenceThresh)
	}
	if cfg.SpeechThresh != 280 {
		t.Errorf("SpeechThresh: want 280, got %v", cfg.SpeechThresh)
	}
	const wantDeg = 0.05
	if cfg.SpeechDegradationLimit != wantDeg {
		t.Errorf("SpeechDegradationLimit: want %v, got %v", wantDeg, cfg.SpeechDegradationLimit)
	}
}

// TestABRunnerNewABRunnerNonNil verifies NewABRunner returns a non-nil runner.
func TestABRunnerNewABRunnerNonNil(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())
	if r == nil {
		t.Fatal("NewABRunner returned nil")
	}
}

// makeSilenceFrame returns a 160-sample zero frame (RMS = 0 → FrameSilence).
func makeSilenceFrame() []int16 {
	return make([]int16, 160)
}

// makeSpeechFrame returns a 160-sample frame with high RMS (~11585) → FrameSpeech.
func makeSpeechFrameAB() []int16 {
	f := make([]int16, 160)
	for i := range f {
		if i%2 == 0 {
			f[i] = 16000
		} else {
			f[i] = -16000
		}
	}
	return f
}

// makeBackgroundFrame returns a 160-sample frame with mid RMS (~100) that falls
// between SilenceThresh (50) and SpeechThresh (280) → FrameBackground.
func makeBackgroundFrame() []int16 {
	f := make([]int16, 160)
	for i := range f {
		if i%2 == 0 {
			f[i] = 141
		} else {
			f[i] = -141
		}
	}
	return f
}

// TestABRunnerProcessFrameSilence verifies that all-zero frames classify as FrameSilence.
func TestABRunnerProcessFrameSilence(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())

	frame := makeSilenceFrame()
	result := r.ProcessFrame(0, frame)
	if result.Class != audio.FrameSilence {
		t.Errorf("expected FrameSilence (0), got %v", result.Class)
	}
	if result.FrameIdx != 0 {
		t.Errorf("expected FrameIdx=0, got %d", result.FrameIdx)
	}
}

// TestABRunnerProcessFrameSpeech verifies that high-RMS frames classify as FrameSpeech.
func TestABRunnerProcessFrameSpeech(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())

	frame := makeSpeechFrameAB()
	result := r.ProcessFrame(1, frame)
	if result.Class != audio.FrameSpeech {
		t.Errorf("expected FrameSpeech (2), got %v", result.Class)
	}
	if result.RawRMS <= 0 {
		t.Errorf("expected positive RawRMS, got %v", result.RawRMS)
	}
	// With identical passthrough suppressors ARMS == BRMS == RawRMS.
	if result.ARMS <= 0 {
		t.Errorf("expected positive ARMS, got %v", result.ARMS)
	}
}

// TestABRunnerProcessFrameBackground verifies mid-RMS frames classify as FrameBackground.
func TestABRunnerProcessFrameBackground(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())

	frame := makeBackgroundFrame()
	result := r.ProcessFrame(2, frame)
	if result.Class != audio.FrameBackground {
		t.Errorf("expected FrameBackground (1), got %v (RawRMS=%v)", result.Class, result.RawRMS)
	}
}

// TestABRunnerProcessFramePassthroughNoViolation verifies that two identical
// passthrough suppressors never produce a BViolation on speech frames.
func TestABRunnerProcessFramePassthroughNoViolation(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())

	frame := makeSpeechFrameAB()
	result := r.ProcessFrame(0, frame)
	if result.BViolation {
		t.Error("expected no BViolation for identical passthrough suppressors")
	}
}

// TestABRunnerSummariseCountsAddUp verifies Summarise totals equal TotalFrames.
func TestABRunnerSummariseCountsAddUp(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())

	frames := [][]int16{
		makeSilenceFrame(),
		makeSpeechFrameAB(),
		makeBackgroundFrame(),
		makeSilenceFrame(),
		makeSpeechFrameAB(),
	}
	results := make([]audio.ABFrameResult, len(frames))
	for i, f := range frames {
		results[i] = r.ProcessFrame(i, f)
	}

	summary := audio.Summarise(results, "A", "B")

	if summary.TotalFrames != len(frames) {
		t.Errorf("TotalFrames: want %d, got %d", len(frames), summary.TotalFrames)
	}
	total := summary.SpeechFrames + summary.BackgroundFrames + summary.SilenceFrames
	if total != summary.TotalFrames {
		t.Errorf("per-class counts %d+%d+%d=%d != TotalFrames=%d",
			summary.SpeechFrames, summary.BackgroundFrames, summary.SilenceFrames, total, summary.TotalFrames)
	}
	if summary.NameA != "A" {
		t.Errorf("NameA: want A, got %s", summary.NameA)
	}
	if summary.NameB != "B" {
		t.Errorf("NameB: want B, got %s", summary.NameB)
	}
}

// TestABRunnerSummariseEmptyResults verifies Summarise handles an empty slice.
func TestABRunnerSummariseEmptyResults(t *testing.T) {
	s := audio.Summarise(nil, "A", "B")
	if s.TotalFrames != 0 {
		t.Errorf("expected TotalFrames=0, got %d", s.TotalFrames)
	}
}

// TestABRunnerSummariseSpeechRMSRatioPassthrough verifies that with identical
// passthrough suppressors, SpeechRMSRatioA and SpeechRMSRatioB are both ~1.0.
func TestABRunnerSummariseSpeechRMSRatioPassthrough(t *testing.T) {
	a := model.NewPassthrough()
	b := model.NewPassthrough()
	r := audio.NewABRunner(a, b, audio.DefaultABConfig())

	frame := makeSpeechFrameAB()
	results := []audio.ABFrameResult{r.ProcessFrame(0, frame)}
	s := audio.Summarise(results, "A", "B")

	const tolerance = 0.01
	if s.SpeechRMSRatioA < 1.0-tolerance || s.SpeechRMSRatioA > 1.0+tolerance {
		t.Errorf("SpeechRMSRatioA: want ~1.0, got %v", s.SpeechRMSRatioA)
	}
	if s.SpeechRMSRatioB < 1.0-tolerance || s.SpeechRMSRatioB > 1.0+tolerance {
		t.Errorf("SpeechRMSRatioB: want ~1.0, got %v", s.SpeechRMSRatioB)
	}
}

// TestABRunnerClassifySilenceViaProcessFrame tests classify(rms) indirectly:
// a zero-RMS frame must always yield FrameSilence regardless of thresholds.
func TestABRunnerClassifySilenceViaProcessFrame(t *testing.T) {
	cfg := audio.ABConfig{SilenceThresh: 100, SpeechThresh: 300, SpeechDegradationLimit: 0.05}
	r := audio.NewABRunner(model.NewPassthrough(), model.NewPassthrough(), cfg)
	result := r.ProcessFrame(0, makeSilenceFrame())
	if result.Class != audio.FrameSilence {
		t.Errorf("expected FrameSilence for zero-RMS frame, got %v", result.Class)
	}
}

// TestABRunnerSNRDeltaZeroForPassthrough tests snrDelta indirectly: with a
// passthrough suppressor rawRMS == processedRMS, so SNRDelta ≈ 0.
func TestABRunnerSNRDeltaZeroForPassthrough(t *testing.T) {
	r := audio.NewABRunner(model.NewPassthrough(), model.NewPassthrough(), audio.DefaultABConfig())
	result := r.ProcessFrame(0, makeSpeechFrameAB())
	const tolerance = 0.001
	if result.SNRDeltaA > tolerance || result.SNRDeltaA < -tolerance {
		t.Errorf("SNRDeltaA: want ~0 for passthrough, got %v", result.SNRDeltaA)
	}
	if result.SNRDeltaB > tolerance || result.SNRDeltaB < -tolerance {
		t.Errorf("SNRDeltaB: want ~0 for passthrough, got %v", result.SNRDeltaB)
	}
}
