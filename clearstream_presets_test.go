package clearstream_test

import (
	"testing"

	"github.com/exotel/clearstream"
)

func TestTelephonyConfig(t *testing.T) {
	cfg := clearstream.TelephonyConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("TelephonyConfig Validate() = %v, want nil", err)
	}
	if !cfg.EnableVAD {
		t.Error("TelephonyConfig: EnableVAD should be true")
	}
	if cfg.MaxConcurrentSessions != 64 {
		t.Errorf("TelephonyConfig: MaxConcurrentSessions = %d, want 64", cfg.MaxConcurrentSessions)
	}
}

func TestFileProcessingConfig(t *testing.T) {
	cfg := clearstream.FileProcessingConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("FileProcessingConfig Validate() = %v, want nil", err)
	}
	if cfg.EnableVAD {
		t.Error("FileProcessingConfig: EnableVAD should be false")
	}
}

func TestContactCenterConfig(t *testing.T) {
	cfg := clearstream.ContactCenterConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ContactCenterConfig Validate() = %v, want nil", err)
	}
	if cfg.MaxConcurrentSessions != 32 {
		t.Errorf("ContactCenterConfig: MaxConcurrentSessions = %d, want 32", cfg.MaxConcurrentSessions)
	}
	if !cfg.EnableVAD {
		t.Error("ContactCenterConfig: EnableVAD should be true")
	}
}

func TestNewWithTelephonyConfig(t *testing.T) {
	cfg := clearstream.TelephonyConfig()
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New(TelephonyConfig()) error = %v", err)
	}
	defer cs.Close()
	if cs.PoolSize() != 64 {
		t.Errorf("PoolSize() = %d, want 64", cs.PoolSize())
	}
}

func TestCallCenterConfig(t *testing.T) {
	cfg := clearstream.CallCenterConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("CallCenterConfig Validate() = %v, want nil", err)
	}
	if cfg.SampleRate != 8000 {
		t.Errorf("SampleRate: got %d, want 8000", cfg.SampleRate)
	}
	if !cfg.EnableVAD {
		t.Error("EnableVAD: expected true")
	}
	if cfg.MaxConcurrentSessions != 100 {
		t.Errorf("MaxConcurrentSessions: got %d, want 100", cfg.MaxConcurrentSessions)
	}
}
