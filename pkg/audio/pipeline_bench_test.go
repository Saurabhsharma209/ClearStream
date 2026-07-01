package audio_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// makeBenchFrames returns n complete 16kHz frames of synthetic PCM
// (sequential int16 values), matching the makeFrame helper's data shape.
func makeBenchFrames(n int) []byte {
	return makeFrame(audio.FrameSizeBytes * n)
}

// BenchmarkProcessFramesBypass measures the passthrough-suppressor,
// no-stages path: the cheapest possible route through the pipeline
// (resample bypass since InputSampleRate defaults to ProcessorSampleRate
// here, no VAD, no AGC, no noise reducer, no limiter).
func BenchmarkProcessFramesBypass(b *testing.B) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
	in := makeBenchFrames(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.ProcessFrames(in, io.Discard); err != nil {
			b.Fatalf("ProcessFrames error: %v", err)
		}
	}
}

// BenchmarkProcessFramesSuppress measures the active-suppression path
// using MockSuppressor, quantifying the cost of the suppressor call itself
// plus the framePool-backed buffer handling around it.
func BenchmarkProcessFramesSuppress(b *testing.B) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewMockSuppressor(),
		Logger:     zap.NewNop(),
	})
	in := makeBenchFrames(1)
	var out bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out.Reset()
		if err := p.ProcessFrames(in, &out); err != nil {
			b.Fatalf("ProcessFrames error: %v", err)
		}
	}
}

// BenchmarkProcessFramesVADSilence measures the VAD-gated silence path,
// where frames below EnergyThreshold bypass the suppressor entirely.
// Input is all-zero PCM (silence), so every frame should take the
// cheap bypass branch inside the pipeline.
func BenchmarkProcessFramesVADSilence(b *testing.B) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewMockSuppressor(),
		Logger:     zap.NewNop(),
		VADConfig: &audio.VADConfig{
			EnergyThreshold: 300,
			HangoverFrames:  8,
		},
	})
	in := make([]byte, audio.FrameSizeBytes) // all-zero = silence
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.ProcessFrames(in, io.Discard); err != nil {
			b.Fatalf("ProcessFrames error: %v", err)
		}
	}
}

// BenchmarkProcessFramesMultiFrame measures throughput across a larger
// batch (50 frames = 500ms of audio) with active suppression, to surface
// any per-call overhead that doesn't show up in single-frame benchmarks.
func BenchmarkProcessFramesMultiFrame(b *testing.B) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewMockSuppressor(),
		Logger:     zap.NewNop(),
	})
	in := makeBenchFrames(50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.ProcessFrames(in, io.Discard); err != nil {
			b.Fatalf("ProcessFrames error: %v", err)
		}
	}
}
