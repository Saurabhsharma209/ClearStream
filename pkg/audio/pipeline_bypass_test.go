package audio_test

import (
	"bytes"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// TestPipelineSetBypass verifies that SetBypass(true) short-circuits the
// suppressor (and the other optional stages it documents) so frames pass
// through unmodified aside from resampling, that Bypassed() reports the
// live state, and that un-bypassing resumes normal processing. SetBypass
// and Bypassed previously had 0%% test coverage despite being a runtime-
// switchable feature (e.g. driven by a per-call disable-enhancement
// control message) -- a regression here (e.g. the bypass flag failing to
// also skip the suppressor, or Bypassed() reading a stale value) would
// have shipped silently.
func TestPipelineSetBypass(t *testing.T) {
	mock := model.NewMockSuppressor()
	mock.Gain = 0.5 // halves every sample when NOT bypassed

	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: mock,
		Logger:     zap.NewNop(),
	})

	if p.Bypassed() {
		t.Fatal("pipeline should not be bypassed by default")
	}

	var sampleValue int16 = 1000
	frame := make([]byte, audio.FrameSizeBytes)
	for i := 0; i < audio.FrameSizeSamples; i++ {
		frame[2*i] = byte(sampleValue)
		frame[2*i+1] = byte(sampleValue >> 8)
	}

	// Baseline: suppressor runs and halves every sample.
	var out bytes.Buffer
	if err := p.ProcessFrames(frame, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	if got := int16(out.Bytes()[0]) | int16(out.Bytes()[1])<<8; got-500 < -1 || got-500 > 1 {
		t.Fatalf("pre-bypass sample: want ~500, got %d", got)
	}
	callsBefore := mock.ProcessCalls

	// Enable bypass mid-stream.
	p.SetBypass(true)
	if !p.Bypassed() {
		t.Fatal("Bypassed() should report true after SetBypass(true)")
	}

	out.Reset()
	if err := p.ProcessFrames(frame, &out); err != nil {
		t.Fatalf("ProcessFrames error while bypassed: %v", err)
	}
	if mock.ProcessCalls != callsBefore {
		t.Errorf("suppressor.Process should not be called while bypassed: calls went from %d to %d", callsBefore, mock.ProcessCalls)
	}
	outBytes := out.Bytes()
	if len(outBytes) != audio.FrameSizeBytes {
		t.Fatalf("output length: want %d, got %d", audio.FrameSizeBytes, len(outBytes))
	}
	for i := 0; i < audio.FrameSizeSamples; i++ {
		if got := int16(outBytes[2*i]) | int16(outBytes[2*i+1])<<8; got != sampleValue {
			t.Fatalf("bypassed sample[%d]: want unmodified %d, got %d", i, sampleValue, got)
		}
	}

	// Disable bypass again -- suppressor should resume running.
	p.SetBypass(false)
	if p.Bypassed() {
		t.Fatal("Bypassed() should report false after SetBypass(false)")
	}
	out.Reset()
	if err := p.ProcessFrames(frame, &out); err != nil {
		t.Fatalf("ProcessFrames error after un-bypass: %v", err)
	}
	if got := int16(out.Bytes()[0]) | int16(out.Bytes()[1])<<8; got-500 < -1 || got-500 > 1 {
		t.Fatalf("post-bypass sample: want ~500, got %d", got)
	}
	if mock.ProcessCalls != callsBefore+1 {
		t.Errorf("suppressor.Process should resume after un-bypass: want %d calls, got %d", callsBefore+1, mock.ProcessCalls)
	}
}
