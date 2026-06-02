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

func TestExotelConfig(t *testing.T) {
	cfg := clearstream.ExotelConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ExotelConfig Validate() = %v, want nil", err)
	}
	if cfg.MaxConcurrentSessions != 32 {
		t.Errorf("ExotelConfig: MaxConcurrentSessions = %d, want 32", cfg.MaxConcurrentSessions)
	}
	if !cfg.EnableVAD {
		t.Error("ExotelConfig: EnableVAD should be true")
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
