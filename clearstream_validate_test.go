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
	validRates := []int{8000, 16000, 32000, 48000}
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

func TestPoolSizeDefault(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New(DefaultConfig()) error: %v", err)
	}
	defer cs.Close()
	if got := cs.PoolSize(); got != 32 {
		t.Errorf("PoolSize() = %d, want 32", got)
	}
}

func TestPoolSizeCustom(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.MaxConcurrentSessions = 8
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New(cfg) error: %v", err)
	}
	defer cs.Close()
	if got := cs.PoolSize(); got != 8 {
		t.Errorf("PoolSize() = %d, want 8", got)
	}
}

func TestValidateDeepFilterServerNoPath(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Model = "deepfilter-server"
	// deepfilter-server does not require a local ModelPath (it calls a remote server)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("deepfilter-server with no ModelPath should pass Validate(), got: %v", err)
	}
}

func TestValidateInvalidSampleRate44100(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.SampleRate = 44100
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for SampleRate=44100 (not in {8000,16000,32000,48000}), got nil")
	}
}
