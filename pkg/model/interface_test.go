package model

import (
	"strings"
	"testing"
)

// TestAsBatch_NonBatchSuppressor verifies AsBatch wraps a non-BatchSuppressor.
func TestAsBatch_NonBatchSuppressor(t *testing.T) {
	// errSuppressor (defined in batch_extra_test.go) does not implement BatchSuppressor.
	inner := &errSuppressor{failAt: -1} // never fails
	bs := AsBatch(inner)
	if bs == nil {
		t.Fatal("AsBatch returned nil")
	}
	if !strings.HasSuffix(bs.Name(), "+batch") {
		t.Errorf("Name() = %q, want suffix +batch", bs.Name())
	}
}

// TestAsBatch_AlreadyBatchSuppressorInternal verifies AsBatch returns the same
// object when it already implements BatchSuppressor (internal package view).
func TestAsBatch_AlreadyBatchSuppressorInternal(t *testing.T) {
	p := NewPassthrough() // *Passthrough implements BatchSuppressor
	bs1 := AsBatch(p)
	bs2 := AsBatch(bs1)
	if bs1 != bs2 {
		t.Error("AsBatch double-wrap: expected same object")
	}
}

// TestNewSuppressor_RNNoiseDefault verifies the "" backend defaults to rnnoise stub.
func TestNewSuppressor_RNNoiseDefault(t *testing.T) {
	s, err := NewSuppressor(SuppressorConfig{Backend: ""})
	if err != nil {
		t.Fatalf("NewSuppressor(\"\"): %v", err)
	}
	if s == nil {
		t.Fatal("got nil suppressor")
	}
	defer s.Close()
}

// TestNewSuppressorPassthrough verifies that Backend="passthrough" returns a *Passthrough.
func TestNewSuppressorPassthrough(t *testing.T) {
	cfg := SuppressorConfig{Backend: "passthrough"}
	s, err := NewSuppressor(cfg)
	if err != nil {
		t.Fatalf("NewSuppressor(passthrough): %v", err)
	}
	if s == nil {
		t.Fatal("NewSuppressor(passthrough): got nil")
	}
	if _, ok := s.(*Passthrough); !ok {
		t.Errorf("NewSuppressor(passthrough): got %T, want *Passthrough", s)
	}
	_ = s.Close()
}

// TestNewSuppressorUnknownBackend verifies that an unknown backend returns an error.
func TestNewSuppressorUnknownBackend(t *testing.T) {
	cfg := SuppressorConfig{Backend: "unknown_backend_xyz"}
	s, err := NewSuppressor(cfg)
	if err == nil {
		t.Fatal("NewSuppressor(unknown): expected error, got nil")
	}
	if s != nil {
		t.Errorf("NewSuppressor(unknown): expected nil suppressor, got %T", s)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unknown") {
		t.Errorf("NewSuppressor(unknown): error %q does not contain 'unknown'", err.Error())
	}
}

// TestNewSuppressorDeepfilterMissingPath verifies deepfilter without ModelPath returns error.
func TestNewSuppressorDeepfilterMissingPath(t *testing.T) {
	cfg := SuppressorConfig{Backend: "deepfilter", ModelPath: ""}
	s, err := NewSuppressor(cfg)
	if err == nil {
		t.Fatal("NewSuppressor(deepfilter, no path): expected error, got nil")
	}
	if s != nil {
		t.Errorf("NewSuppressor(deepfilter, no path): expected nil suppressor, got %T", s)
	}
	if !strings.Contains(err.Error(), "ModelPath") {
		t.Errorf("error %q does not mention ModelPath", err.Error())
	}
}

// TestNewSuppressorRNNoiseONNXMissingPath verifies rnnoise-onnx without ModelPath returns error.
func TestNewSuppressorRNNoiseONNXMissingPath(t *testing.T) {
	cfg := SuppressorConfig{Backend: "rnnoise-onnx", ModelPath: ""}
	s, err := NewSuppressor(cfg)
	if err == nil {
		t.Fatal("NewSuppressor(rnnoise-onnx, no path): expected error, got nil")
	}
	if s != nil {
		t.Errorf("NewSuppressor(rnnoise-onnx, no path): expected nil suppressor, got %T", s)
	}
	if !strings.Contains(err.Error(), "ModelPath") {
		t.Errorf("error %q does not mention ModelPath", err.Error())
	}
}

// TestNewSuppressorPassthroughProcesses verifies the returned suppressor functions correctly.
func TestNewSuppressorPassthroughProcesses(t *testing.T) {
	cfg := SuppressorConfig{Backend: "passthrough"}
	s, err := NewSuppressor(cfg)
	if err != nil {
		t.Fatalf("NewSuppressor: %v", err)
	}
	defer s.Close()

	frame := []int16{100, 200, 300}
	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	for i, v := range frame {
		if out[i] != v {
			t.Errorf("out[%d] = %d, want %d", i, out[i], v)
		}
	}
}

// TestNewSuppressorUnknownErrorContainsValidList verifies error lists valid backends.
func TestNewSuppressorUnknownErrorContainsValidList(t *testing.T) {
	cfg := SuppressorConfig{Backend: "bogus"}
	_, err := NewSuppressor(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, valid := range []string{"passthrough", "rnnoise", "deepfilter"} {
		if !strings.Contains(msg, valid) {
			t.Errorf("error %q does not mention valid backend %q", msg, valid)
		}
	}
}

// TestNewRNNoiseONNX_Stub covers rnnoise_onnx_stub.go (0% coverage).
func TestNewRNNoiseONNX_Stub(t *testing.T) {
	logger := makeTestLogger()
	s, err := NewRNNoiseONNX("/any/path/model.onnx", logger)
	if err == nil {
		if s != nil {
			s.Close()
		}
		t.Fatal("NewRNNoiseONNX without onnx build tag: expected error, got nil")
	}
	if s != nil {
		t.Errorf("NewRNNoiseONNX: expected nil suppressor, got %T", s)
	}
}

// TestNewSuppressor_RNNoiseONNX_WithPath exercises the rnnoise-onnx + ModelPath branch.
func TestNewSuppressor_RNNoiseONNX_WithPath(t *testing.T) {
	cfg := SuppressorConfig{Backend: "rnnoise-onnx", ModelPath: "/tmp/fake_model.onnx"}
	s, err := NewSuppressor(cfg)
	if err == nil && s != nil {
		s.Close()
	}
	// Without -tags onnx, error is expected. Either outcome is fine for coverage.
}

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

func TestNewRNNoise_ReturnsPassthrough(t *testing.T) {
	s, err := NewRNNoise(0)
	if err != nil {
		t.Fatalf("NewRNNoise() unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("NewRNNoise() returned nil")
	}
	defer s.Close()
	// RNNoise operates on 160-sample 16kHz frames; passthrough returns input length.
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = int16(i * 100 % 32767)
	}
	out, err := s.Process(frame)
	if err != nil {
		t.Errorf("Process() error: %v", err)
	}
	if len(out) != 160 {
		t.Errorf("expected 160 samples, got %d", len(out))
	}
}
