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

// BenchmarkPassthroughLargeFrame benchmarks Passthrough.Process with 1024-sample
// frames (64ms at 16kHz) to verify the interface handles non-standard frame sizes.
func BenchmarkPassthroughLargeFrame(b *testing.B) {
	sup, _ := model.NewSuppressor(model.SuppressorConfig{Backend: "passthrough"})
	defer sup.Close()
	frame := make([]int16, 1024)
	for i := range frame {
		frame[i] = int16(math.Sin(float64(i)*0.05) * 16000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sup.Process(frame)
	}
}

// BenchmarkMockSuppressor benchmarks MockSuppressor.Process with a 160-sample
// frame and Gain=1.0, providing a baseline for test-double overhead.
func BenchmarkMockSuppressor(b *testing.B) {
	m := model.NewMockSuppressor()
	m.Gain = 1.0
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(i * 100)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Process(frame)
	}
}

// TestSuppressorInterfaceCompliance is a table-driven test that verifies every
// available suppressor satisfies the Suppressor contract:
//   - Name() returns a non-empty string
//   - Process() returns a frame of the same length as the input
//   - Reset() does not panic
//   - Close() returns nil
func TestSuppressorInterfaceCompliance(t *testing.T) {
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(i)
	}

	tests := []struct {
		name string
		sup  model.Suppressor
	}{
		{"passthrough", model.NewPassthrough()},
		{"mock", model.NewMockSuppressor()},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.sup.Name() == "" {
				t.Error("Name() must return a non-empty string")
			}
			out, err := tc.sup.Process(frame)
			if err != nil {
				t.Errorf("Process() returned unexpected error: %v", err)
			}
			if len(out) != len(frame) {
				t.Errorf("Process() returned %d samples, want %d", len(out), len(frame))
			}
			tc.sup.Reset() // must not panic
			if err := tc.sup.Close(); err != nil {
				t.Errorf("Close() returned unexpected error: %v", err)
			}
		})
	}
}
