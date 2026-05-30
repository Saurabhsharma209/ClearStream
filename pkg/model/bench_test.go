package model_test

import (
	"testing"
	"github.com/exotel/clearstream/pkg/model"
)

func BenchmarkPassthrough(b *testing.B) {
	p := model.NewPassthrough()
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(i * 100)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Process(frame)
		if err != nil {
			b.Fatal(err)
		}
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
