package audio

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

func TestProcess48kPassthrough(t *testing.T) {
	p := NewPipeline(PipelineConfig{Suppressor: nil})
	frame := make([]int16, Frame48kSamples)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}
	out, err := p.Process48k(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != Frame48kSamples {
		t.Fatalf("want %d samples, got %d", Frame48kSamples, len(out))
	}
}

func TestProcess48kWithMock(t *testing.T) {
	mock := model.NewMockSuppressor()
	p := NewPipeline(PipelineConfig{Suppressor: mock})
	frame := make([]int16, Frame48kSamples)
	for i := range frame {
		frame[i] = 3000
	}
	out, err := p.Process48k(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != Frame48kSamples {
		t.Fatalf("want %d samples, got %d", Frame48kSamples, len(out))
	}
	if mock.ProcessCalls != 1 {
		t.Errorf("want 1 suppressor call, got %d", mock.ProcessCalls)
	}
	s := p.Stats()
	if s.FramesProcessed != 1 {
		t.Errorf("FramesProcessed: want 1, got %d", s.FramesProcessed)
	}
	if s.FramesSuppressed != 1 {
		t.Errorf("FramesSuppressed: want 1, got %d", s.FramesSuppressed)
	}
}

type silenceVAD struct{}

func (s *silenceVAD) IsSpeech(_ []int16) bool { return false }
func (s *silenceVAD) Reset()                  {}

func TestProcess48kVAD(t *testing.T) {
	mock := model.NewMockSuppressor()
	p := NewPipeline(PipelineConfig{Suppressor: mock, VAD: &silenceVAD{}})
	frame := make([]int16, Frame48kSamples)
	out, err := p.Process48k(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != Frame48kSamples {
		t.Fatalf("want %d samples, got %d", Frame48kSamples, len(out))
	}
	if mock.ProcessCalls != 0 {
		t.Errorf("suppressor should not be called on silence, got %d calls", mock.ProcessCalls)
	}
	if p.Stats().FramesSilent != 1 {
		t.Errorf("FramesSilent: want 1, got %d", p.Stats().FramesSilent)
	}
}

func TestProcess48kWrongLength(t *testing.T) {
	p := NewPipeline(PipelineConfig{})
	_, err := p.Process48k(make([]int16, 160))
	if err == nil {
		t.Fatal("expected error for wrong-length input")
	}
}
