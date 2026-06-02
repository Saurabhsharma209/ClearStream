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
