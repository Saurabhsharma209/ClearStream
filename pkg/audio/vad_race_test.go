package audio

import (
	"sync"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

// TestPipelineSetVADThresholdRaceGuard is a regression test: Pipeline.SetVADThreshold
// (and turnEndTracker.setThreshold) used to write VAD.ThresholdRMS directly with zero
// synchronization, so a mid-call sensitivity change on one goroutine (e.g. a
// control-plane handler reacting to a live UI adjustment) raced with VAD.IsSpeech
// reading ThresholdRMS concurrently on the audio-processing goroutine inside
// ProcessFrames. go test -race must exercise both concurrently and pass cleanly
// after the fix (VAD.SetThresholdRMS/threshold() now use atomic load/store),
// exactly mirroring TestPipelineSetAGCTargetRejectsNonPositive's coverage of the
// analogous AGC race fixed in agc.go.
func TestPipelineSetVADThresholdRaceGuard(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SampleRate:        16000,
		Channels:          1,
		Suppressor:        model.NewPassthrough(),
		VAD:               DefaultVAD(),
		TurnEndBufferSize: 4,
	})

	frame := make([]byte, FrameSizeBytes)
	for i := range frame {
		frame[i] = byte(i)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Goroutine 1: mid-call sensitivity changes, as if from a control-plane
	// handler reacting to a live UI adjustment.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			p.SetVADThreshold(float64(200 + i%100))
		}
		close(stop)
	}()

	// Goroutine 2: the audio-processing hot path, calling ProcessFrames
	// (which calls VAD.IsSpeech per frame) concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var out discardWriter
		for {
			select {
			case <-stop:
				return
			default:
				_ = p.ProcessFrames(frame, &out)
			}
		}
	}()

	wg.Wait()
}

// discardWriter is a minimal io.Writer that discards all bytes written,
// avoiding a bytes.Buffer allocation per frame in the race test's hot loop.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestVADSetThresholdRMSRejectsNonPositive mirrors AGC.SetTargetRMS's guard:
// non-positive thresholds must be ignored so IsSpeech never silently starts
// comparing against an invalid (zero/negative) threshold.
func TestVADSetThresholdRMSRejectsNonPositive(t *testing.T) {
	v := DefaultVAD()
	if v.threshold() != 300 {
		t.Fatalf("setup: expected initial threshold 300, got %v", v.threshold())
	}

	v.SetThresholdRMS(0)
	if v.threshold() != 300 {
		t.Errorf("SetThresholdRMS(0) must be ignored, got threshold = %v, want unchanged 300", v.threshold())
	}

	v.SetThresholdRMS(-50)
	if v.threshold() != 300 {
		t.Errorf("SetThresholdRMS(-50) must be ignored, got threshold = %v, want unchanged 300", v.threshold())
	}

	v.SetThresholdRMS(450)
	if v.threshold() != 450 {
		t.Errorf("SetThresholdRMS(450) should apply, got threshold = %v, want 450", v.threshold())
	}
}
