package clearstream_test

import (
	"strings"
	"testing"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
)

func TestSDKWithVAD(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.EnableVAD = true
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() with EnableVAD=true failed: %v", err)
	}
	defer cs.Close()
	p := cs.Pipeline()
	if p == nil {
		t.Fatal("Pipeline() returned nil")
	}
}

func TestSDKWithAdaptiveVAD(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.EnableVAD = true
	cfg.AdaptiveVAD = true
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() with AdaptiveVAD=true failed: %v", err)
	}
	defer cs.Close()
	p := cs.Pipeline()
	if p == nil {
		t.Fatal("Pipeline() returned nil")
	}
}

func TestPipelineStatsString(t *testing.T) {
	s := audio.PipelineStats{
		FramesProcessed:  100,
		FramesSuppressed: 50,
		FramesSilent:     50,
		SuppressRatio:    0.5,
		AvgLatencyMs:     1.23,
	}
	str := s.String()
	if !strings.Contains(str, "100") {
		t.Errorf("String() %q does not contain '100'", str)
	}
	if !strings.Contains(str, "50.0%") {
		t.Errorf("String() %q does not contain '50.0%%'", str)
	}
}
