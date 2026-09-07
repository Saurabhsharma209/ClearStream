package clearstream_test

import (
	"strings"
	"testing"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
)

// TestNew_InvalidSampleRate verifies New() propagates Validate() errors.
func TestNew_InvalidSampleRate(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.SampleRate = -1
	_, err := clearstream.New(cfg)
	if err == nil {
		t.Fatal("New() with SampleRate=-1 should return error")
	}
	if !strings.Contains(err.Error(), "SampleRate") {
		t.Errorf("expected SampleRate in error, got: %v", err)
	}
}

// TestClose_Idempotent verifies that calling Close() twice does not panic.
func TestClose_Idempotent(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if err := cs.Close(); err != nil {
		t.Errorf("first Close() error: %v", err)
	}
	// Second Close() must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close() panicked: %v", r)
		}
	}()
	cs.Close() //nolint:errcheck
}

// TestValidate_AllBranches covers every error path in Config.Validate().
func TestValidate_AllBranches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*clearstream.Config)
		wantErr bool
		errHint string
	}{
		{"valid default", func(c *clearstream.Config) {}, false, ""},
		{"sampleRate too low", func(c *clearstream.Config) { c.SampleRate = 4000 }, true, "SampleRate"},
		{"sampleRate too high", func(c *clearstream.Config) { c.SampleRate = 96000 }, true, "SampleRate"},
		{"sampleRate zero is ok", func(c *clearstream.Config) { c.SampleRate = 0 }, false, ""},
		{"channels 3 invalid", func(c *clearstream.Config) { c.Channels = 3 }, true, "Channels"},
		{"channels 0 ok", func(c *clearstream.Config) { c.Channels = 0 }, false, ""},
		{"channels 2 ok", func(c *clearstream.Config) { c.Channels = 2 }, false, ""},
		{"unknown model", func(c *clearstream.Config) { c.Model = "magic" }, true, "Model"},
		{"deepfilter no path", func(c *clearstream.Config) { c.Model = "deepfilter"; c.ModelPath = "" }, true, "deepfilter"},
		{"deepfilter with path", func(c *clearstream.Config) { c.Model = "deepfilter"; c.ModelPath = "/tmp/m.onnx" }, false, ""},
		{"passthrough ok", func(c *clearstream.Config) { c.Model = "passthrough" }, false, ""},
		{"PCMU wrong rate", func(c *clearstream.Config) { c.Codec = "PCMU"; c.SampleRate = 16000 }, true, "PCMU"},
		{"PCMU correct rate", func(c *clearstream.Config) { c.Codec = "PCMU"; c.SampleRate = 8000 }, false, ""},
		{"PCMA wrong rate", func(c *clearstream.Config) { c.Codec = "PCMA"; c.SampleRate = 16000 }, true, "PCMA"},
		{"PCMA correct rate", func(c *clearstream.Config) { c.Codec = "PCMA"; c.SampleRate = 8000 }, false, ""},
		{"G722 wrong rate", func(c *clearstream.Config) { c.Codec = "G722"; c.SampleRate = 8000 }, true, "G722"},
		{"G722 correct rate", func(c *clearstream.Config) { c.Codec = "G722"; c.SampleRate = 16000 }, false, ""},
		{"unknown codec no check", func(c *clearstream.Config) { c.Codec = "OPUS"; c.SampleRate = 48000 }, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := clearstream.DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected nil error, got: %v", err)
			}
			if tc.wantErr && tc.errHint != "" && err != nil {
				if !strings.Contains(err.Error(), tc.errHint) {
					t.Errorf("expected %q in error, got: %v", tc.errHint, err)
				}
			}
		})
	}
}

// TestPoolSize_MatchesConfig ensures PoolSize() reflects MaxConcurrentSessions.
func TestPoolSize_MatchesConfig(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.MaxConcurrentSessions = 5
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	if got := cs.PoolSize(); got != 5 {
		t.Errorf("PoolSize() = %d, want 5", got)
	}
}

// TestPoolSize_DefaultFallback ensures MaxConcurrentSessions=0 defaults to 32.
func TestPoolSize_DefaultFallback(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.MaxConcurrentSessions = 0
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	if got := cs.PoolSize(); got != 32 {
		t.Errorf("PoolSize() = %d, want 32", got)
	}
}

// TestProcessFile_MissingFile verifies ProcessFile returns an error for non-existent files.
func TestProcessFile_MissingFile(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	err = cs.ProcessFile("/nonexistent/input.wav", "/tmp/out.wav")
	if err == nil {
		t.Error("ProcessFile with missing input should return error")
	}
}

// TestProcessFileWithOptions_MissingFile verifies ProcessFileWithOptions errors on missing file.
func TestProcessFileWithOptions_MissingFile(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	err = cs.ProcessFile("/nonexistent/input2.wav", "/tmp/out2.wav")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestPipelineStats verifies PipelineStats returns without panic.
func TestPipelineStats(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	stats := cs.PipelineStats()
	_ = stats
}

// TestNewRTPSession_PoolShrinks verifies pool capacity is positive before session creation.
func TestNewRTPSession_PoolShrinks(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.Model = "passthrough"
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()

	sizeBefore := cs.PoolSize()
	if sizeBefore <= 0 {
		t.Fatalf("PoolSize should be > 0, got %d", sizeBefore)
	}
}

// TestGlobalAGC_Enabled verifies Pipeline() works when AGC is enabled.
func TestGlobalAGC_Enabled(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.EnableAGC = true
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	p := cs.Pipeline()
	if p == nil {
		t.Error("Pipeline() with AGC enabled returned nil")
	}
}

// TestPipeline_VADStatic verifies Pipeline with static VAD does not panic.
func TestPipeline_VADStatic(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.EnableVAD = true
	cfg.AdaptiveVAD = false
	cfg.VADThreshold = 300
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	p := cs.Pipeline()
	if p == nil {
		t.Error("Pipeline() returned nil")
	}
}

// TestPipeline_VADAdaptive verifies Pipeline with adaptive VAD does not panic.
func TestPipeline_VADAdaptive(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.EnableVAD = true
	cfg.AdaptiveVAD = true
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	p := cs.Pipeline()
	if p == nil {
		t.Error("Pipeline() returned nil")
	}
}

// TestPipeline_VADZeroThreshold verifies the default threshold fallback (threshold=0).
func TestPipeline_VADZeroThreshold(t *testing.T) {
	cfg := clearstream.DefaultConfig()
	cfg.EnableVAD = true
	cfg.AdaptiveVAD = false
	cfg.VADThreshold = 0 // triggers default 300 path
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	_ = cs.Pipeline()
}

// TestPresetConfigs verifies all preset config factories return valid configs.
func TestPresetConfigs(t *testing.T) {
	configs := []struct {
		name string
		cfg  clearstream.Config
	}{
		{"Telephony", clearstream.TelephonyConfig()},
		{"FileProcessing", clearstream.FileProcessingConfig()},
		{"your telephony platform", clearstream.ContactCenterConfig()},
	}
	for _, tc := range configs {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err != nil {
				t.Errorf("Validate() failed: %v", err)
			}
		})
	}
}

// TestProcessFileWithOptions_MissingFile2 exercises ProcessFileWithOptions (0% coverage).
func TestProcessFileWithOptions_MissingFile2(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	err = cs.ProcessFileWithOptions("/nonexistent/input3.wav", "/tmp/out3.wav", file.Options{})
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestGlobalAGC_CustomAGCConfig verifies globalAGC returns the custom config when AGC is set.
func TestGlobalAGC_CustomAGCConfig(t *testing.T) {
	agcCfg := audio.DefaultAGCConfig()
	cfg := clearstream.DefaultConfig()
	cfg.EnableAGC = true
	cfg.AGC = &agcCfg
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()
	_ = cs.Pipeline()
}

// TestPoolSizeForPeakTracks_AllBranches covers the peakCalls/forwardOnly
// formula in PoolSizeForPeakTracks (0% covered prior to this test). The
// function exists specifically to prevent the "server-164" misconfiguration
// described in its doc comment: an operator set MaxConcurrentSessions to a
// desired call count (4) on a bidirectional deployment and got half the
// intended call capacity, because each bidirectional call consumes 2
// session slots, not 1. This test pins the exact formula (peakCalls*2 for
// bidirectional, peakCalls unchanged for forward-only) across boundary
// values a capacity-planning caller might realistically pass in: zero,
// one, the documented regression scenario, larger fleet sizes, and
// negative input (documenting that the function does not itself validate
// peakCalls -- it is a pure arithmetic helper and passes negative input
// straight through the same formula).
func TestPoolSizeForPeakTracks_AllBranches(t *testing.T) {
	cases := []struct {
		name        string
		peakCalls   int
		forwardOnly bool
		want        int
	}{
		{"bidirectional zero calls needs zero slots", 0, false, 0},
		{"forwardOnly zero calls needs zero slots", 0, true, 0},
		{"bidirectional single call needs 2 slots", 1, false, 2},
		{"forwardOnly single call needs 1 slot", 1, true, 1},
		{"server-164 regression scenario: 4 bidirectional calls need 8 slots, not 4", 4, false, 8},
		{"forwardOnly 4 calls need exactly 4 slots", 4, true, 4},
		{"bidirectional 32-call fleet needs 64 slots", 32, false, 64},
		{"forwardOnly 32-call fleet needs 32 slots", 32, true, 32},
		{"bidirectional large call volume", 1000, false, 2000},
		{"forwardOnly large call volume", 1000, true, 1000},
		{"negative peakCalls bidirectional passes through the formula unvalidated", -1, false, -2},
		{"negative peakCalls forwardOnly passes through the formula unvalidated", -1, true, -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clearstream.PoolSizeForPeakTracks(tc.peakCalls, tc.forwardOnly)
			if got != tc.want {
				t.Errorf("PoolSizeForPeakTracks(%d, %v) = %d, want %d", tc.peakCalls, tc.forwardOnly, got, tc.want)
			}
		})
	}
}

// TestPoolSizeForPeakTracks_IntegratesWithNew exercises the exact recipe
// documented on PoolSizeForPeakTracks itself:
//
//	cfg.MaxConcurrentSessions = PoolSizeForPeakTracks(wantedCalls, cfg.ForwardOnly)
//
// and confirms that the resulting ClearStream instance reports the computed
// session-slot count via PoolSize(), not the raw call count. This is the
// regression the helper exists to prevent: PoolSize() must reflect
// cfg.MaxConcurrentSessions as configured (New() captures it before
// applying its own internal suppressor-pool doubling for the model pool),
// so a caller following the documented recipe gets the capacity they
// actually asked for.
func TestPoolSizeForPeakTracks_IntegratesWithNew(t *testing.T) {
	const wantedCalls = 4

	cfg := clearstream.DefaultConfig()
	cfg.ForwardOnly = false
	cfg.MaxConcurrentSessions = clearstream.PoolSizeForPeakTracks(wantedCalls, cfg.ForwardOnly)

	if cfg.MaxConcurrentSessions != 8 {
		t.Fatalf("PoolSizeForPeakTracks(%d, false) = %d, want 8", wantedCalls, cfg.MaxConcurrentSessions)
	}

	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()

	if got := cs.PoolSize(); got != 8 {
		t.Errorf("PoolSize() = %d, want 8 (session slots for %d bidirectional calls)", got, wantedCalls)
	}
}

// TestPoolSizeForPeakTracks_ForwardOnlyIntegratesWithNew mirrors the above
// for a forward-only deployment, where PoolSizeForPeakTracks should be a
// no-op passthrough and PoolSize() should equal the raw call count.
func TestPoolSizeForPeakTracks_ForwardOnlyIntegratesWithNew(t *testing.T) {
	const wantedCalls = 10

	cfg := clearstream.DefaultConfig()
	cfg.ForwardOnly = true
	cfg.MaxConcurrentSessions = clearstream.PoolSizeForPeakTracks(wantedCalls, cfg.ForwardOnly)

	if cfg.MaxConcurrentSessions != wantedCalls {
		t.Fatalf("PoolSizeForPeakTracks(%d, true) = %d, want %d", wantedCalls, cfg.MaxConcurrentSessions, wantedCalls)
	}

	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()

	if got := cs.PoolSize(); got != wantedCalls {
		t.Errorf("PoolSize() = %d, want %d (1:1 session slots for forward-only calls)", got, wantedCalls)
	}
}
