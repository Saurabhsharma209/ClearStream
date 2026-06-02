// Package loadtest provides in-process load testing for ClearStream pipelines.
package loadtest

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// Result holds load test metrics.
type Result struct {
	Sessions   int
	Frames     uint64
	Errors     uint64
	DurationMs float64
	FPS        float64 // frames per second
}

// Run executes sessions concurrent pipeline sessions, each processing frames frames.
// Uses passthrough suppressor — swap for real model to benchmark AI overhead.
func Run(ctx context.Context, sessions, frames int) Result {
	var totalFrames, totalErrors uint64
	start := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, sessions)
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p := audio.NewPipeline(audio.PipelineConfig{
				SampleRate: 16000,
				Channels:   1,
				Suppressor: model.NewPassthrough(),
			})
			frame := make([]byte, audio.FrameSizeBytes)
			for j := 0; j < frames; j++ {
				if ctx.Err() != nil {
					break
				}
				var out bytes.Buffer
				if err := p.ProcessFrames(frame, &out); err != nil {
					atomic.AddUint64(&totalErrors, 1)
				} else {
					atomic.AddUint64(&totalFrames, 1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds() * 1000
	total := atomic.LoadUint64(&totalFrames)
	return Result{
		Sessions:   sessions,
		Frames:     total,
		Errors:     atomic.LoadUint64(&totalErrors),
		DurationMs: elapsed,
		FPS:        float64(total) / (elapsed / 1000),
	}
}
