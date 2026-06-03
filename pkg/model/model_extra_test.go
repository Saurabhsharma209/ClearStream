package model

import (
	"testing"
)

func TestDefaultSuppressorConfig(t *testing.T) {
	cfg := DefaultSuppressorConfig()
	if cfg.Backend != "passthrough" {
		t.Errorf("expected backend=passthrough, got %q", cfg.Backend)
	}
}

func TestNewSuppressor_AllBranches(t *testing.T) {
	cases := []struct {
		name    string
		cfg     SuppressorConfig
		wantErr bool
	}{
		{"passthrough", SuppressorConfig{Backend: "passthrough"}, false},
		{"rnnoise (stub)", SuppressorConfig{Backend: "rnnoise"}, false},
		{"empty (defaults to rnnoise stub)", SuppressorConfig{Backend: ""}, false},
		{"deepfilter no path", SuppressorConfig{Backend: "deepfilter", ModelPath: ""}, true},
		{"deepfilter with path (stub)", SuppressorConfig{Backend: "deepfilter", ModelPath: "/tmp/m.onnx"}, true},
		{"unknown backend", SuppressorConfig{Backend: "unknownXYZ"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSuppressor(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (suppressor=%v)", s)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if s != nil {
					s.Close()
				}
			}
		})
	}
}

func TestPassthrough_ProcessIdentity(t *testing.T) {
	p := NewPassthrough()
	input := []int16{100, -200, 300, 32767, -32768, 0}
	out, err := p.Process(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(input) {
		t.Fatalf("length mismatch: want %d, got %d", len(input), len(out))
	}
	for i, v := range input {
		if out[i] != v {
			t.Errorf("sample[%d]: want %d, got %d", i, v, out[i])
		}
	}
}

func TestPassthrough_EmptyFrame(t *testing.T) {
	p := NewPassthrough()
	out, err := p.Process([]int16{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %d samples", len(out))
	}
}

func TestPassthrough_Reset(t *testing.T) {
	p := NewPassthrough()
	p.Reset()
}

func TestPassthrough_Close(t *testing.T) {
	p := NewPassthrough()
	if err := p.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestPassthrough_Name(t *testing.T) {
	p := NewPassthrough()
	if got := p.Name(); got != "passthrough" {
		t.Errorf("Name() = %q, want \"passthrough\"", got)
	}
}

func TestSuppressorPool_CloseIdempotent(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Errorf("first Close() error: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close() panicked: %v", r)
		}
	}()
	pool.Close()
}

func TestSuppressorPool_ZeroSize(t *testing.T) {
	_, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 0)
	if err == nil {
		t.Error("expected error for pool size 0, got nil")
	}
}

func TestSuppressorPool_NegativeSize(t *testing.T) {
	_, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, -1)
	if err == nil {
		t.Error("expected error for pool size -1, got nil")
	}
}

func TestSuppressorPool_AcquireReleaseCycle(t *testing.T) {
	pool, err := NewSuppressorPool(SuppressorConfig{Backend: "passthrough"}, 2)
	if err != nil {
		t.Fatalf("NewSuppressorPool: %v", err)
	}
	defer pool.Close()

	s1 := pool.Acquire()
	s2 := pool.Acquire()

	pool.Release(s1)
	s3 := pool.Acquire()
	if s3 == nil {
		t.Error("expected non-nil suppressor after release")
	}

	pool.Release(s2)
	pool.Release(s3)
}

func TestMockSuppressor_Counters(t *testing.T) {
	m := NewMockSuppressor()
	frame := []int16{1, 2, 3}

	if m.ProcessCalls != 0 || m.ResetCalls != 0 {
		t.Fatalf("initial counters should be 0")
	}

	m.Process(frame)
	m.Process(frame)
	m.Reset()

	if m.ProcessCalls != 2 {
		t.Errorf("ProcessCalls: want 2, got %d", m.ProcessCalls)
	}
	if m.ResetCalls != 1 {
		t.Errorf("ResetCalls: want 1, got %d", m.ResetCalls)
	}
}

func TestNewRNNoise_ReturnsPassthrough(t *testing.T) {
	s, err := NewRNNoise()
	if err != nil {
		t.Fatalf("NewRNNoise() unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("NewRNNoise() returned nil")
	}
	defer s.Close()
	out, err := s.Process([]int16{100, 200, 300})
	if err != nil {
		t.Errorf("Process() error: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("expected 3 samples, got %d", len(out))
	}
}
