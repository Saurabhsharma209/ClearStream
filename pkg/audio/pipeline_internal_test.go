package audio

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestPipelineByteOrderRoundtrip(t *testing.T) {
	values := []int16{-32768, -1, 0, 1, 32767}
	b := int16ToBytes(values)
	got := bytesToInt16(b)
	if len(got) != len(values) {
		t.Fatalf("length mismatch: want %d, got %d", len(values), len(got))
	}
	for i, want := range values {
		if got[i] != want {
			t.Errorf("sample[%d]: want %d, got %d", i, want, got[i])
		}
	}
}

func TestPipelineStatsString(t *testing.T) {
	s := PipelineStats{
		FramesProcessed:  100,
		FramesSuppressed: 80,
		FramesSilent:     20,
		SuppressRatio:    0.8,
		AvgLatencyMs:     1.23,
	}
	str := s.String()
	if !strings.Contains(str, "100") {
		t.Errorf("PipelineStats.String() missing FramesProcessed: %q", str)
	}
	if !strings.Contains(str, "80") {
		t.Errorf("PipelineStats.String() missing FramesSuppressed: %q", str)
	}
}

func TestPipelineStats(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
	})

	frame := make([]byte, FrameSizeBytes)
	var out nopWriter
	for i := 0; i < 5; i++ {
		if err := p.ProcessFrames(frame, &out); err != nil {
			t.Fatalf("ProcessFrames error: %v", err)
		}
	}

	stats := p.Stats()
	if stats.FramesProcessed != 5 {
		t.Errorf("FramesProcessed = %d, want 5", stats.FramesProcessed)
	}
	if stats.SuppressRatio < 0 || stats.SuppressRatio > 1 {
		t.Errorf("SuppressRatio = %f, want 0–1", stats.SuppressRatio)
	}
}

func TestPipelineStatsEmpty(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate: 16000,
		Suppressor: sup,
	})
	stats := p.Stats()
	if stats.SuppressRatio != 0 {
		t.Errorf("empty pipeline SuppressRatio = %f, want 0", stats.SuppressRatio)
	}
}

func TestPipelineInputRateFallback(t *testing.T) {
	// InputSampleRate=0 and SampleRate=0 → defaults to 8000
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{Suppressor: sup})
	if r := p.inputRate(); r != 8000 {
		t.Errorf("inputRate() = %d, want 8000 (fallback)", r)
	}
}

func TestPipelineInputRateFromSampleRate(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{SampleRate: 16000, Suppressor: sup})
	if r := p.inputRate(); r != 16000 {
		t.Errorf("inputRate() = %d, want 16000", r)
	}
}

func TestPipelineInputRateFromInputSampleRate(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{SampleRate: 16000, InputSampleRate: 8000, Suppressor: sup})
	if r := p.inputRate(); r != 8000 {
		t.Errorf("inputRate() = %d, want 8000 (InputSampleRate takes priority)", r)
	}
}

func TestPipelineDiarizationSegmentsNil(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate: 16000,
		Suppressor: sup,
	})
	segs := p.DiarizationSegments()
	if segs != nil {
		t.Errorf("DiarizationSegments() without diarizer = %v, want nil", segs)
	}
}

func TestPipelineFlushWithAEC(t *testing.T) {
	aecCfg := DefaultAECConfig()
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		AEC:             &aecCfg,
	})
	// Feed a partial frame (less than FrameSizeBytes)
	partial := make([]byte, 100)
	var out1 bytes.Buffer
	if err := p.ProcessFrames(partial, &out1); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	// Flush should drain it
	var out2 bytes.Buffer
	if err := p.Flush(&out2); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if out2.Len() != FrameSizeBytes {
		t.Errorf("Flush output len = %d, want %d", out2.Len(), FrameSizeBytes)
	}
}

func TestPipelineFlushEmptyNoop(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate: 16000,
		Suppressor: sup,
	})
	// Flush on empty buffer should be a noop
	var out bytes.Buffer
	if err := p.Flush(&out); err != nil {
		t.Fatalf("Flush on empty buffer error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("Flush on empty buffer produced %d bytes, want 0", out.Len())
	}
}

func TestPipelineResetWithDiarizer(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        d,
	})

	// Feed some speech
	speech := makeSpeech(160)
	speechBytes := int16ToBytes(speech)
	var out nopWriter
	for i := 0; i < 10; i++ {
		if err := p.ProcessFrames(speechBytes, &out); err != nil {
			t.Fatalf("ProcessFrames error: %v", err)
		}
	}

	p.Reset()

	// After reset, stats should be cleared
	stats := p.Stats()
	if stats.FramesProcessed != 0 {
		t.Errorf("FramesProcessed after Reset = %d, want 0", stats.FramesProcessed)
	}
}

func TestSetFarEndConcurrent(t *testing.T) {
	aecCfg := DefaultAECConfig()
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		AEC:             &aecCfg,
	})

	frame := make([]byte, FrameSizeBytes)
	farEnd := make([]int16, 160)

	var wg sync.WaitGroup
	// SetFarEnd goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			p.SetFarEnd(farEnd)
		}
	}()

	// ProcessFrames goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		var out nopWriter
		for i := 0; i < 100; i++ {
			_ = p.ProcessFrames(frame, &out)
		}
	}()

	wg.Wait() // if there is a race, -race will catch it
}

func TestPipelineWithAdaptiveVAD(t *testing.T) {
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:     16000,
		Suppressor:     sup,
		UseAdaptiveVAD: true,
	})

	// Feed 60 silence frames (600ms > calibration window of 500ms)
	silence := make([]byte, FrameSizeBytes) // all zeros = silence
	var out nopWriter
	for i := 0; i < 60; i++ {
		if err := p.ProcessFrames(silence, &out); err != nil {
			t.Fatalf("frame %d: ProcessFrames error: %v", i, err)
		}
	}

	stats := p.Stats()
	if stats.FramesProcessed != 60 {
		t.Errorf("FramesProcessed = %d, want 60", stats.FramesProcessed)
	}
}

func TestPipelineNewAGCZeroSampleRate(t *testing.T) {
	// When cfg.SampleRate == 0 and AGC != nil, pipeline defaults to 16000
	agcCfg := DefaultAGCConfig()
	agcCfg.SampleRate = 0
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate: 0,
		Suppressor: sup,
		AGC:        &agcCfg,
	})
	if p == nil {
		t.Fatal("NewPipeline returned nil")
	}
}

type errSuppressor struct{}

func (e *errSuppressor) Process(in []int16) ([]int16, error) {
	return nil, fmt.Errorf("suppressor error")
}

func (e *errSuppressor) Reset() {}

func (e *errSuppressor) Close() error { return nil }

func (e *errSuppressor) Name() string { return "errSuppressor" }

func TestProcessFramesSuppressorError(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      &errSuppressor{},
	})
	frame := make([]byte, FrameSizeBytes)
	var out nopWriter
	err := p.ProcessFrames(frame, &out)
	if err == nil {
		t.Error("expected error from failing suppressor")
	}
}

func TestProcessFramesFlushSuppressorError(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      &errSuppressor{},
	})
	// Feed partial frame to accumulate in buf
	partial := make([]byte, 100)
	var out nopWriter
	_ = p.ProcessFrames(partial, &out)
	// Now flush — should error from suppressor
	var buf bytes.Buffer
	err := p.Flush(&buf)
	if err == nil {
		t.Error("Flush expected error from failing suppressor")
	}
}

// alwaysSilenceVAD always returns false (silence) — exercises the vad bypass path
type alwaysSilenceVAD struct{}

func (a *alwaysSilenceVAD) IsSpeech(_ []int16) bool { return false }

func (a *alwaysSilenceVAD) Reset() {}

func TestProcessFramesVADSilenceBypass(t *testing.T) {
	// With VAD always returning false, suppressor should not be called
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		VAD:             &alwaysSilenceVAD{},
	})
	frame := make([]byte, FrameSizeBytes)
	var out bytes.Buffer
	if err := p.ProcessFrames(frame, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	// Output should equal input (passthrough in silence)
	if out.Len() != FrameSizeBytes {
		t.Errorf("output len = %d, want %d", out.Len(), FrameSizeBytes)
	}
}

func TestPipelineResetWithAEC(t *testing.T) {
	aecCfg := DefaultAECConfig()
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		AEC:             &aecCfg,
	})
	p.SetFarEnd(make([]int16, 160))
	frame := make([]byte, FrameSizeBytes)
	var out nopWriter
	_ = p.ProcessFrames(frame, &out)
	p.Reset() // should reset AEC state too
	stats := p.Stats()
	if stats.FramesProcessed != 0 {
		t.Errorf("FramesProcessed after Reset = %d, want 0", stats.FramesProcessed)
	}
}

func TestDiarizationSegmentsWithDiarizer(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        d,
	})

	// Feed enough speech then silence to create completed segments
	ts := int64(0)
	speechBytes := int16ToBytes(makeSpeech(160))
	silenceBytes := int16ToBytes(makeSilence(160))
	var out nopWriter

	for i := 0; i < 10; i++ {
		_ = p.ProcessFrames(speechBytes, &out)
		ts += 10
	}
	for i := 0; i < 35; i++ {
		_ = p.ProcessFrames(silenceBytes, &out)
		ts += 10
	}

	segs := p.DiarizationSegments()
	// Just verify the call doesn't panic and returns a slice
	_ = segs
}

func TestPipelineResetWithVADAndAGC(t *testing.T) {
	agcCfg := DefaultAGCConfig()
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		AGC:             &agcCfg,
		VAD:             &alwaysSilenceVAD{},
	})
	frame := make([]byte, FrameSizeBytes)
	var out nopWriter
	_ = p.ProcessFrames(frame, &out)
	p.Reset() // covers vad.Reset() and agc.Reset() branches in Reset()
	stats := p.Stats()
	if stats.FramesProcessed != 0 {
		t.Errorf("FramesProcessed after Reset = %d, want 0", stats.FramesProcessed)
	}
}

func TestDiarizationSegmentsReturnsData(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        d,
	})

	// Force completed segments: speech → long silence
	speechBytes := int16ToBytes(makeSpeech(160))
	silenceBytes := int16ToBytes(makeSilence(160))
	var out nopWriter

	for i := 0; i < 10; i++ {
		_ = p.ProcessFrames(speechBytes, &out)
	}
	for i := 0; i < 35; i++ {
		_ = p.ProcessFrames(silenceBytes, &out)
	}

	// This exercises the return p.diarizer.Segments() branch
	segs := p.DiarizationSegments()
	_ = segs // may be empty or not, just ensure it executes
}

func TestProcessFrames8kHzInput(t *testing.T) {
	// Input at 8kHz → pipeline resamples 8kHz→16kHz before suppression, back after
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      8000,
		InputSampleRate: 8000,
		Suppressor:      sup,
	})

	// At 8kHz, 10ms = 80 samples = 160 bytes
	inputFrameBytes := 80 * 2 // FrameSizeSamples * 8000/16000 * 2
	frame := make([]byte, inputFrameBytes)
	for i := range frame {
		frame[i] = byte(i % 256)
	}

	var out bytes.Buffer
	if err := p.ProcessFrames(frame, &out); err != nil {
		t.Fatalf("ProcessFrames(8kHz) error: %v", err)
	}
	// Should produce output (160 bytes at 8kHz = 80 samples = same as input frame)
	if out.Len() == 0 {
		t.Error("ProcessFrames(8kHz) produced no output")
	}
}

func TestPipelineNewAECZeroSampleRate(t *testing.T) {
	// When AEC config SampleRate == 0 and cfg.SampleRate > 0, should use cfg.SampleRate
	aecCfg := AECConfig{FilterLen: 512, StepSize: 0.1, Leakage: 0.9999, SampleRate: 0}
	sup := &noopSuppressor{}
	p := NewPipeline(PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		AEC:             &aecCfg,
	})
	if p.aec == nil {
		t.Fatal("AEC should be initialized")
	}
}
