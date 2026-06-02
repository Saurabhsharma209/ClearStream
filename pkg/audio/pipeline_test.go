package audio_test

import (
	"bytes"
	"math"
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

// makePCMFrame builds a FrameSizeBytes frame where every int16 sample == value.
func makePCMFrame(value int16) []byte {
	b := make([]byte, audio.FrameSizeBytes)
	for i := 0; i < audio.FrameSizeSamples; i++ {
		b[2*i] = byte(value)
		b[2*i+1] = byte(value >> 8)
	}
	return b
}

// TestPipelineAdaptiveVADCalibration verifies that a pipeline with
// UseAdaptiveVAD:true produces output for every frame (calibration frames are
// treated as silence and passed through; post-calibration frames go through the
// suppressor). No errors must occur and total output must equal total input.
func TestPipelineAdaptiveVADCalibration(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate:     16000,
		Channels:       1,
		Suppressor:     model.NewPassthrough(),
		Logger:         zap.NewNop(),
		UseAdaptiveVAD: true,
	})

	silenceFrame := makePCMFrame(10)   // very low energy — well below any noise floor
	speechFrame := makePCMFrame(20000) // loud speech

	var out bytes.Buffer

	// Feed 50 calibration frames of silence.
	for i := 0; i < 50; i++ {
		if err := p.ProcessFrames(silenceFrame, &out); err != nil {
			t.Fatalf("calibration frame %d: ProcessFrames error: %v", i, err)
		}
	}

	// Feed 1 more silence frame (post-calibration).
	if err := p.ProcessFrames(silenceFrame, &out); err != nil {
		t.Fatalf("post-calibration silence: ProcessFrames error: %v", err)
	}

	// Feed 1 loud speech frame.
	if err := p.ProcessFrames(speechFrame, &out); err != nil {
		t.Fatalf("speech frame: ProcessFrames error: %v", err)
	}

	const totalFrames = 52
	wantBytes := totalFrames * audio.FrameSizeBytes
	if out.Len() != wantBytes {
		t.Errorf("output length: want %d bytes (%d frames), got %d", wantBytes, totalFrames, out.Len())
	}
}

// TestPipelineStatsSuppressRatio verifies FramesProcessed, FramesSilent,
// FramesSuppressed, and SuppressRatio are correctly tracked.
func TestPipelineStatsSuppressRatio(t *testing.T) {
	// ThresholdRMS=500; silence samples=10 (RMS~10), speech samples=5000 (RMS~5000).
	vad := &audio.VAD{ThresholdRMS: 500, HangoverFrames: 0}

	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
		VAD:        vad,
	})

	silenceFrame := makePCMFrame(10)
	speechFrame := makePCMFrame(5000)

	var out bytes.Buffer

	// 10 silence frames.
	for i := 0; i < 10; i++ {
		if err := p.ProcessFrames(silenceFrame, &out); err != nil {
			t.Fatalf("silence frame %d error: %v", i, err)
		}
	}
	// 10 speech frames.
	for i := 0; i < 10; i++ {
		if err := p.ProcessFrames(speechFrame, &out); err != nil {
			t.Fatalf("speech frame %d error: %v", i, err)
		}
	}

	stats := p.Stats()

	if stats.FramesProcessed != 20 {
		t.Errorf("FramesProcessed: want 20, got %d", stats.FramesProcessed)
	}
	if stats.FramesSilent != 10 {
		t.Errorf("FramesSilent: want 10, got %d", stats.FramesSilent)
	}
	if stats.FramesSuppressed != 10 {
		t.Errorf("FramesSuppressed: want 10, got %d", stats.FramesSuppressed)
	}
	if math.Abs(stats.SuppressRatio-0.5) > 0.01 {
		t.Errorf("SuppressRatio: want ~0.5, got %.4f", stats.SuppressRatio)
	}
}

// TestPipelineReset verifies that Reset() clears stats counters so that only
// the frames processed after the reset are reflected in Stats().
func TestPipelineReset(t *testing.T) {
	p := newTestPipeline()
	frame := makePCMFrame(100)
	var out bytes.Buffer

	// Process 5 frames then reset.
	for i := 0; i < 5; i++ {
		if err := p.ProcessFrames(frame, &out); err != nil {
			t.Fatalf("pre-reset frame %d error: %v", i, err)
		}
	}
	p.Reset()
	out.Reset()

	// Process 5 more frames after reset.
	for i := 0; i < 5; i++ {
		if err := p.ProcessFrames(frame, &out); err != nil {
			t.Fatalf("post-reset frame %d error: %v", i, err)
		}
	}

	stats := p.Stats()
	if stats.FramesProcessed != 5 {
		t.Errorf("FramesProcessed after Reset: want 5, got %d", stats.FramesProcessed)
	}
}
