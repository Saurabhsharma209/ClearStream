package clearstream_test

import (
	"testing"

	"github.com/exotel/clearstream"
)

func TestValidateDefault(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig() should pass Validate(), got: %v", err)
	}
}

func TestValidateSampleRateTooLow(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.SampleRate = 4000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for SampleRate=4000, got nil")
	}
}

func TestValidateSampleRateTooHigh(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.SampleRate = 96000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for SampleRate=96000, got nil")
	}
}

func TestValidateSampleRateValid(t *testing.T) {
	validRates := []int{8000, 16000, 44100, 48000}
	for _, rate := range validRates {
		cfg := clearstream.DefaultConfig()
		cfg.SampleRate = rate
		if err := cfg.Validate(); err != nil {
			t.Errorf("SampleRate=%d should be valid, got: %v", rate, err)
		}
	}
}

func TestValidateChannels(t *testing.T) {
	for _, ch := range []int{0, 1, 2} {
		cfg := clearstream.DefaultConfig()
		cfg.Channels = ch
		if err := cfg.Validate(); err != nil {
			t.Errorf("Channels=%d should be valid, got: %v", ch, err)
		}
	}
	cfg := clearstream.DefaultConfig()
	cfg.Channels = 3
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for Channels=3, got nil")
	}
}

func TestValidateUnknownModel(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Model = "neural"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for Model=\"neural\", got nil")
	}
}

func TestValidateDeepFilterNoPath(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Model = "deepfilter"
	cfg.ModelPath = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for deepfilter with no ModelPath, got nil")
	}
}

func TestValidateDeepFilterWithPath(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Model = "deepfilter"
	cfg.ModelPath = "/tmp/model.onnx"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("deepfilter with ModelPath should pass Validate(), got: %v", err)
	}
}
