package model_test

import (
	"math"
	"sync"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

func BenchmarkPassthrough(b *testing.B) {
	sup, _ := model.NewSuppressor(model.SuppressorConfig{Backend: "passthrough"})
	defer sup.Close()
	frame := make([]int16, 160) // 10ms at 16kHz
	for i := range frame {
		frame[i] = int16(math.Sin(float64(i)*0.1) * 16000)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sup.Process(frame)
	}
}

// BenchmarkRNNoiseFrameLatency measures RNNoise (or its passthrough fallback) per-frame overhead.
// Run: CGO_ENABLED=1 go test -tags cgo -bench=BenchmarkRNNoiseFrameLatency ./pkg/model/
func BenchmarkRNNoiseFrameLatency(b *testing.B) {
	// Uses passthrough as fallback when CGo not available — still measures overhead
	sup, _ := model.NewSuppressor(model.SuppressorConfig{Backend: "rnnoise"})
	defer sup.Close()
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(float64(i%100) * 200)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sup.Process(frame)
	}
}

func TestPassthroughRoundtrip(t *testing.T) {
	p := model.NewPassthrough()
	in := make([]int16, 160)
	for i := range in {
		in[i] = int16(i)
	}
	out, err := p.Process(in)
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("length mismatch: got %d want %d", len(out), len(in))
	}
	for i, v := range out {
		if v != in[i] {
			t.Errorf("sample[%d]: got %d want %d", i, v, in[i])
		}
	}
}

func TestNewSuppressorPassthrough(t *testing.T) {
	s, err := model.NewSuppressor(model.SuppressorConfig{Backend: "passthrough"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name() != "passthrough" {
		t.Errorf("expected passthrough, got %s", s.Name())
	}
	s.Close()
}

func TestNewSuppressorUnknown(t *testing.T) {
	_, err := model.NewSuppressor(model.SuppressorConfig{Backend: "unknown-backend"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestSuppressorConcurrentReset(t *testing.T) {
	sup, err := model.NewSuppressor(model.SuppressorConfig{Backend: "passthrough"})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()

	var wg sync.WaitGroup
	frame := make([]int16, 160)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); sup.Process(frame) }()
		go func() { defer wg.Done(); sup.Reset() }()
	}
	wg.Wait() // must not race or panic
}
