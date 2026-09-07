package clearstream_test

import (
	"testing"

	"github.com/exotel/clearstream"
)

func TestIndiaTelephonyConfig(t *testing.T) {
	cfg := clearstream.IndiaTelephonyConfig()
	if cfg.SampleRate != 8000 {
		t.Errorf("IndiaTelephonyConfig().SampleRate = %d, want 8000", cfg.SampleRate)
	}
	if !cfg.EnableVAD {
		t.Error("IndiaTelephonyConfig().EnableVAD = false, want true")
	}
	if cfg.MaxConcurrentSessions != 64 {
		t.Errorf("IndiaTelephonyConfig().MaxConcurrentSessions = %d, want 64", cfg.MaxConcurrentSessions)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("IndiaTelephonyConfig() failed Validate(): %v", err)
	}
}

func TestWidebandConfig(t *testing.T) {
	cfg := clearstream.WidebandConfig()
	if cfg.SampleRate != 16000 {
		t.Errorf("WidebandConfig().SampleRate = %d, want 16000", cfg.SampleRate)
	}
	if !cfg.EnableVAD {
		t.Error("WidebandConfig().EnableVAD = false, want true")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("WidebandConfig() failed Validate(): %v", err)
	}
}

func TestValidate_G722MustBe16kHz(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Codec = "G722"
	cfg.SampleRate = 8000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for Codec=G722 with SampleRate=8000, got nil")
	}
}

func TestValidate_PCMUMustBe8kHz(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Codec = "PCMU"
	cfg.SampleRate = 16000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for Codec=PCMU with SampleRate=16000, got nil")
	}
}

func TestValidate_PCMAMustBe8kHz(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Codec = "PCMA"
	cfg.SampleRate = 16000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for Codec=PCMA with SampleRate=16000, got nil")
	}
}

func TestValidate_CodecRateMatch(t *testing.T) {
	tests := []struct {
		codec string
		rate  int
	}{
		{"PCMU", 8000},
		{"PCMA", 8000},
		{"G722", 16000},
	}
	for _, tt := range tests {
		cfg := clearstream.DefaultConfig()
		cfg.Codec = tt.codec
		cfg.SampleRate = tt.rate
		if err := cfg.Validate(); err != nil {
			t.Errorf("Codec=%s SampleRate=%d should pass Validate(), got: %v", tt.codec, tt.rate, err)
		}
	}
}
