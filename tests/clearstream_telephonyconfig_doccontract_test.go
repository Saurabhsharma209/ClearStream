package clearstream_test

import (
	"testing"

	"github.com/exotel/clearstream"
)

// TestTelephonyConfig_MatchesDocumentedContract locks TelephonyConfig() to the
// behavior its own doc comment promises: "optimized for telephony (8kHz G.711
// calls). Enables VAD and AGC; uses passthrough suppressor by default."
//
// Prior to this fix, TelephonyConfig() only set EnableVAD/AdaptiveVAD and
// MaxConcurrentSessions on top of DefaultConfig() -- it silently inherited
// DefaultConfig()'s SampleRate (16000, not the documented 8000), Model
// ("rnnoise", not the documented "passthrough"), and never set EnableAGC at
// all, despite the doc comment explicitly claiming "Enables VAD and AGC".
// A caller following the doc comment and building an 8kHz G.711 (e.g. PCMU)
// deployment off TelephonyConfig() would silently run the pipeline at 16kHz
// instead of 8kHz -- a real, cross-package audio-rate mismatch, not just a
// coverage gap -- and would get no AGC despite the doc promising it.
func TestTelephonyConfig_MatchesDocumentedContract(t *testing.T) {
	cfg := clearstream.TelephonyConfig()

	if cfg.SampleRate != 8000 {
		t.Errorf("TelephonyConfig().SampleRate = %d, want 8000 (doc: %q)", cfg.SampleRate, "optimized for telephony (8kHz G.711 calls)")
	}
	if cfg.Model != "passthrough" {
		t.Errorf("TelephonyConfig().Model = %q, want %q (doc: %q)", cfg.Model, "passthrough", "uses passthrough suppressor by default")
	}
	if !cfg.EnableAGC {
		t.Error("TelephonyConfig().EnableAGC = false, want true (doc: \"Enables VAD and AGC\")")
	}
	if !cfg.EnableVAD {
		t.Error("TelephonyConfig().EnableVAD = false, want true (doc: \"Enables VAD and AGC\")")
	}

	// A real 8kHz G.711 PCMU deployment built exactly per the doc comment's
	// example must pass Validate() -- this is the concrete failure mode the
	// bug produced: SampleRate silently stuck at 16000 fails PCMU's
	// required-8000 check.
	cfg.Codec = "PCMU"
	if err := cfg.Validate(); err != nil {
		t.Errorf("TelephonyConfig() with Codec=PCMU failed Validate(): %v (SampleRate=%d)", err, cfg.SampleRate)
	}
}

// TestContactCenterConfig_SetsDocumentedCodec locks ContactCenterConfig()'s
// doc-promised "PCMA (A-law) codec" -- previously the Codec field was never
// set, so the documented codec/rate cross-check Validate() offers never
// actually ran for this preset.
func TestContactCenterConfig_SetsDocumentedCodec(t *testing.T) {
	cfg := clearstream.ContactCenterConfig()
	if cfg.Codec != "PCMA" {
		t.Errorf("ContactCenterConfig().Codec = %q, want %q", cfg.Codec, "PCMA")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("ContactCenterConfig() failed Validate(): %v", err)
	}
}
