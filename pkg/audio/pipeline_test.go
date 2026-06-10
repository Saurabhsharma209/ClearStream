package audio_test

import (
	"bytes"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// newTestPipeline creates a pipeline with a passthrough suppressor.
func newTestPipeline() *audio.Pipeline {
	return audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
}

// makeFrame returns n bytes of synthetic PCM (sequential int16 values).
func makeFrame(n int) []byte {
	b := make([]byte, n)
	for i := 0; i < n/2; i++ {
		v := int16(i % 256)
		b[2*i] = byte(v)
		b[2*i+1] = byte(v >> 8)
	}
	return b
}

func TestPipelineTwoCompleteFrames(t *testing.T) {
	p := newTestPipeline()
	// 2 full frames = 2 * FrameSizeBytes = 640 bytes
	input := makeFrame(audio.FrameSizeBytes * 2)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	if out.Len() != audio.FrameSizeBytes*2 {
		t.Errorf("expected %d output bytes, got %d", audio.FrameSizeBytes*2, out.Len())
	}
}

func TestPipelinePartialFrameBuffered(t *testing.T) {
	p := newTestPipeline()
	// Feed 1.5 frames - should only output 1 frame, buffer the rest
	input := makeFrame(audio.FrameSizeBytes + audio.FrameSizeBytes/2)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	if out.Len() != audio.FrameSizeBytes {
		t.Errorf("expected %d output bytes (1 frame), got %d", audio.FrameSizeBytes, out.Len())
	}
}

func TestPipelineFlushDrainsPartial(t *testing.T) {
	p := newTestPipeline()
	// Feed a partial frame (140 bytes < 320)
	input := makeFrame(140)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected 0 bytes before flush, got %d", out.Len())
	}
	if err := p.Flush(&out); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if out.Len() != audio.FrameSizeBytes {
		t.Errorf("expected %d bytes after flush (padded frame), got %d", audio.FrameSizeBytes, out.Len())
	}
}

func TestPipelineResetClearsBuffer(t *testing.T) {
	p := newTestPipeline()
	// Feed partial frame
	if err := p.ProcessFrames(makeFrame(100), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	p.Reset()
	// After reset, flush should produce nothing (buffer cleared)
	var out bytes.Buffer
	if err := p.Flush(&out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("expected 0 bytes after reset+flush, got %d", out.Len())
	}
}

func TestPipelinePassthroughFidelity(t *testing.T) {
	p := newTestPipeline()
	// Passthrough must not modify samples
	input := makeFrame(audio.FrameSizeBytes)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, out.Bytes()) {
		t.Error("passthrough pipeline modified audio samples")
	}
}

// TestPipelineStatsAccumulation verifies that Stats().FramesProcessed,
// FramesSilent, and FramesSuppressed are updated correctly after feeding
// batches of speech and silence frames through the pipeline.
//
// It uses a static VAD with HangoverFrames=0 so that the speech/silence
// classification is deterministic: frames with high RMS (>= ThresholdRMS)
// are speech, frames with zero energy are silence.
//
// InputSampleRate is set to 16000 (== ProcessorSampleRate) so that no
// resampling occurs and FrameSizeBytes (320) is used directly.
func TestPipelineStatsAccumulation(t *testing.T) {
	const threshold = 5000.0 // RMS threshold; speech frames will be well above this

	vad := &audio.VAD{
		ThresholdRMS:   threshold,
		HangoverFrames: 0, // no hangover - silence is silent immediately
	}

	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000, // no resampling; frame size stays FrameSizeBytes
		Channels:        1,
		Suppressor:      model.NewPassthrough(),
		VAD:             vad,
		Logger:          zap.NewNop(),
	})

	// speechFrame: all samples = 10000, RMS = 10000 > threshold, classified as speech
	speechFrame := make([]byte, audio.FrameSizeBytes)
	for i := 0; i < audio.FrameSizeSamples; i++ {
		var s int16 = 10000
		speechFrame[2*i] = byte(s)
		speechFrame[2*i+1] = byte(s >> 8)
	}

	// silenceFrame: all samples = 0, RMS = 0 < threshold, classified as silence
	silenceFrame := make([]byte, audio.FrameSizeBytes)

	const speechCount = 5
	const silenceCount = 3

	var out bytes.Buffer

	// Feed speech frames one at a time.
	for i := 0; i < speechCount; i++ {
		if err := p.ProcessFrames(speechFrame, &out); err != nil {
			t.Fatalf("speech frame %d: ProcessFrames error: %v", i, err)
		}
	}
	s := p.Stats()
	if s.FramesProcessed != speechCount {
		t.Errorf("after speech: FramesProcessed want %d, got %d", speechCount, s.FramesProcessed)
	}
	if s.FramesSuppressed != speechCount {
		t.Errorf("after speech: FramesSuppressed want %d, got %d", speechCount, s.FramesSuppressed)
	}
	if s.FramesSilent != 0 {
		t.Errorf("after speech: FramesSilent want 0, got %d", s.FramesSilent)
	}

	// Feed silence frames one at a time.
	for i := 0; i < silenceCount; i++ {
		if err := p.ProcessFrames(silenceFrame, &out); err != nil {
			t.Fatalf("silence frame %d: ProcessFrames error: %v", i, err)
		}
	}
	s = p.Stats()
	total := uint64(speechCount + silenceCount)
	if s.FramesProcessed != total {
		t.Errorf("after silence: FramesProcessed want %d, got %d", total, s.FramesProcessed)
	}
	if s.FramesSuppressed != speechCount {
		t.Errorf("after silence: FramesSuppressed want %d, got %d", speechCount, s.FramesSuppressed)
	}
	if s.FramesSilent != silenceCount {
		t.Errorf("after silence: FramesSilent want %d, got %d", silenceCount, s.FramesSilent)
	}
	if s.FramesProcessed != s.FramesSuppressed+s.FramesSilent {
		t.Errorf("invariant: FramesProcessed(%d) != FramesSuppressed(%d)+FramesSilent(%d)",
			s.FramesProcessed, s.FramesSuppressed, s.FramesSilent)
	}

	// Output must contain all frames (passthrough suppressor writes every frame).
	wantBytes := int(total) * audio.FrameSizeBytes
	if out.Len() != wantBytes {
		t.Errorf("output bytes: want %d, got %d", wantBytes, out.Len())
	}
}

func TestPipelineWithMock(t *testing.T) {
	mock := model.NewMockSuppressor()
	mock.Gain = 0.5

	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: mock,
		Logger:     zap.NewNop(),
	})

	const numFrames = 5
	var sampleValue int16 = 1000
	var expectedValue int16 = 500

	// Build a frame of 160 samples all equal to sampleValue
	frame := make([]byte, audio.FrameSizeBytes)
	for i := 0; i < audio.FrameSizeSamples; i++ {
		frame[2*i] = byte(sampleValue)
		frame[2*i+1] = byte(sampleValue >> 8)
	}

	var out bytes.Buffer
	for i := 0; i < numFrames; i++ {
		if err := p.ProcessFrames(frame, &out); err != nil {
			t.Fatalf("frame %d: ProcessFrames error: %v", i, err)
		}
	}

	if mock.ProcessCalls != numFrames {
		t.Errorf("ProcessCalls: want %d, got %d", numFrames, mock.ProcessCalls)
	}

	// Verify output samples are within ±1 of expectedValue
	outBytes := out.Bytes()
	if len(outBytes) != numFrames*audio.FrameSizeBytes {
		t.Fatalf("output length: want %d, got %d", numFrames*audio.FrameSizeBytes, len(outBytes))
	}
	for i := 0; i < numFrames*audio.FrameSizeSamples; i++ {
		got := int16(outBytes[2*i]) | int16(outBytes[2*i+1])<<8
		diff := got - expectedValue
		if diff < -1 || diff > 1 {
			t.Errorf("sample[%d]: want ~%d, got %d", i, expectedValue, got)
			break
		}
	}
}
