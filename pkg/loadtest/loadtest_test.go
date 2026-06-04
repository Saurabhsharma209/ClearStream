package loadtest_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/loadtest"
	"github.com/exotel/clearstream/pkg/model"
)

func TestLoadTest10Sessions(t *testing.T) {
	ctx := context.Background()
	result := loadtest.Run(ctx, 10, 100)
	t.Logf("10-session result: sessions=%d frames=%d errors=%d duration=%.1fms fps=%.0f",
		result.Sessions, result.Frames, result.Errors, result.DurationMs, result.FPS)
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
	if result.Frames != 1000 {
		t.Errorf("expected 1000 frames, got %d", result.Frames)
	}
	if result.FPS < 1000 {
		t.Errorf("expected FPS > 1000 (passthrough is fast), got %.0f", result.FPS)
	}
}

func TestLoadTest50Sessions(t *testing.T) {
	ctx := context.Background()
	result := loadtest.Run(ctx, 50, 50)
	t.Logf("50-session result: sessions=%d frames=%d errors=%d duration=%.1fms fps=%.0f",
		result.Sessions, result.Frames, result.Errors, result.DurationMs, result.FPS)
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
	if result.Frames != 2500 {
		t.Errorf("expected 2500 frames, got %d", result.Frames)
	}
}

// TestLoadTest500Sessions is the Day-22 regression guard: 500 concurrent
// pipeline sessions, each processing 20 frames (200ms of audio at 10ms/frame).
// At passthrough speeds, this should complete in well under 30 seconds with 0 errors.
// The test validates that the pipeline is safe under high goroutine concurrency.
func TestLoadTest500Sessions(t *testing.T) {
	ctx := context.Background()
	const sessions = 500
	const framesPerSession = 20
	result := loadtest.Run(ctx, sessions, framesPerSession)
	t.Logf("500-session result: sessions=%d frames=%d errors=%d duration=%.1fms fps=%.0f",
		result.Sessions, result.Frames, result.Errors, result.DurationMs, result.FPS)
	if result.Errors != 0 {
		t.Errorf("expected 0 errors from 500 sessions, got %d", result.Errors)
	}
	wantFrames := uint64(sessions * framesPerSession)
	if result.Frames != wantFrames {
		t.Errorf("expected %d total frames, got %d", wantFrames, result.Frames)
	}
	// Sanity: passthrough must be at least 10x real-time (10,000 fps).
	if result.FPS < 10000 {
		t.Errorf("passthrough FPS too low (%.0f < 10000) — possible goroutine scheduling issue", result.FPS)
	}
}

// BenchmarkPipeline500 benchmarks a single pipeline across 500 sequential
// iterations so we can track per-session overhead independently of concurrency.
func BenchmarkPipeline500(b *testing.B) {
	pipelines := make([]*audio.Pipeline, 500)
	for i := range pipelines {
		pipelines[i] = audio.NewPipeline(audio.PipelineConfig{
			SampleRate: 16000,
			Channels:   1,
			Suppressor: model.NewPassthrough(),
		})
	}
	frame := make([]byte, audio.FrameSizeBytes)
	b.SetBytes(audio.FrameSizeBytes * 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range pipelines {
			var out bytes.Buffer
			if err := p.ProcessFrames(frame, &out); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkPipeline(b *testing.B) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})
	frame := make([]byte, audio.FrameSizeBytes)
	b.SetBytes(audio.FrameSizeBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out bytes.Buffer
		if err := p.ProcessFrames(frame, &out); err != nil {
			b.Fatal(err)
		}
	}
}
