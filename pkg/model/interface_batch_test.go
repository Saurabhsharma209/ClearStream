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
