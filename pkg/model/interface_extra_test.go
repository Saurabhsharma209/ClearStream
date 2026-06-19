package model

import (
	"strings"
	"testing"
)

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
