package compat_test

import (
	"strings"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/compat"
)

// TestPlatformConstants verifies all Platform constants have expected string values.
func TestPlatformConstants(t *testing.T) {
	cases := []struct {
		platform compat.Platform
		want     string
	}{
		{compat.PlatformAsterisk, "asterisk"},
		{compat.PlatformFreeSWITCH, "freeswitch"},
		{compat.PlatformKamailio, "kamailio"},
		{compat.PlatformRTPEngine, "rtpengine"},
		{compat.PlatformJanus, "janus"},
		{compat.PlatformVSIP, "vsip"},
		{compat.PlatformGenericWSS, "wss"},
		{compat.PlatformGenericRTP, "rtp"},
	}
	for _, tc := range cases {
		if string(tc.platform) != tc.want {
			t.Errorf("Platform %q: got %q, want %q", tc.platform, string(tc.platform), tc.want)
		}
	}
}

// TestParseVersion checks that version strings parse correctly.
func TestParseVersion(t *testing.T) {
	cases := []struct {
		input     string
		wantMajor int
		wantMinor int
		wantPatch int
	}{
		{"18.20.1", 18, 20, 1},
		{"20.0.0", 20, 0, 0},
		{"1.10.12", 1, 10, 12},
		{"mr11.5.1", 11, 5, 1}, // "mr" prefix stripped
		{"", 0, 0, 0},          // empty → zero version
	}
	for _, tc := range cases {
		v := compat.ParseVersion(tc.input)
		if v.Major != tc.wantMajor || v.Minor != tc.wantMinor || v.Patch != tc.wantPatch {
			t.Errorf("ParseVersion(%q) = {%d,%d,%d}, want {%d,%d,%d}",
				tc.input, v.Major, v.Minor, v.Patch,
				tc.wantMajor, tc.wantMinor, tc.wantPatch)
		}
	}
}

// TestVersionGTE verifies version comparison logic.
func TestVersionGTE(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"20.0.0", "18.0.0", true},
		{"18.0.0", "20.0.0", false},
		{"20.0.0", "20.0.0", true}, // equal is GTE
		{"20.1.0", "20.0.0", true},
		{"20.0.1", "20.0.0", true},
		{"1.10.12", "1.10.0", true},
		{"11.5.1", "11.0.0", true},
	}
	for _, tc := range cases {
		a := compat.ParseVersion(tc.a)
		b := compat.ParseVersion(tc.b)
		if got := a.GTE(b); got != tc.want {
			t.Errorf("ParseVersion(%q).GTE(ParseVersion(%q)) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestRecommendAsterisk checks Asterisk profiles for known version strings.
func TestRecommendAsterisk(t *testing.T) {
	cases := []struct {
		version          string
		wantPathContains string
		wantCodecPrefer  audio.Codec
		wantAGC          bool
		wantErr          bool
	}{
		{"20.19.0", "EAGI", audio.CodecG711A, false, false},
		{"18.0.0", "EAGI", audio.CodecG711A, false, false},
		{"16.0.0", "EAGI only", audio.CodecG711A, false, false},
		{"15.0.0", "", "", false, true},              // below minimum — error expected
		{"", "EAGI", audio.CodecG711A, false, false}, // empty = latest advice
	}
	for _, tc := range cases {
		p, err := compat.Recommend(compat.PlatformAsterisk, tc.version)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Asterisk %q: expected error, got nil", tc.version)
			}
			continue
		}
		if err != nil {
			t.Errorf("Asterisk %q: unexpected error: %v", tc.version, err)
			continue
		}
		if !strings.Contains(p.IntegrationPath, tc.wantPathContains) {
			t.Errorf("Asterisk %q: IntegrationPath %q does not contain %q",
				tc.version, p.IntegrationPath, tc.wantPathContains)
		}
		if p.PreferredCodec != tc.wantCodecPrefer {
			t.Errorf("Asterisk %q: PreferredCodec = %q, want %q",
				tc.version, p.PreferredCodec, tc.wantCodecPrefer)
		}
		if p.AGCRecommended != tc.wantAGC {
			t.Errorf("Asterisk %q: AGCRecommended = %v, want %v", tc.version, p.AGCRecommended, tc.wantAGC)
		}
	}
}

// TestRecommendyour telephony platform verifies the your telephony platform profile has correct codec and path.
func TestRecommendyour telephony platform(t *testing.T) {
	p, err := compat.Recommend(compat.PlatformVSIP, "")
	if err != nil {
		t.Fatalf("your telephony platform Recommend: unexpected error: %v", err)
	}
	if p.PreferredCodec != audio.CodecG711A {
		t.Errorf("your telephony platform: PreferredCodec = %q, want %q", p.PreferredCodec, audio.CodecG711A)
	}
	if p.AGCRecommended {
		t.Errorf("your telephony platform: AGCRecommended should be false (PSTN levels are stable)")
	}
	if !strings.Contains(p.IntegrationPath, "RTP proxy") {
		t.Errorf("your telephony platform: IntegrationPath %q should mention RTP proxy", p.IntegrationPath)
	}
	// Must support both PCMA and PCMU
	found := map[audio.Codec]bool{}
	for _, c := range p.SupportedCodecs {
		found[c] = true
	}
	if !found[audio.CodecG711A] {
		t.Error("your telephony platform: SupportedCodecs missing CodecG711A (PCMA)")
	}
	if !found[audio.CodecG711U] {
		t.Error("your telephony platform: SupportedCodecs missing CodecG711U (PCMU)")
	}
}

// TestRecommendJanus checks Janus profile — Opus + AGC enabled.
func TestRecommendJanus(t *testing.T) {
	p, err := compat.Recommend(compat.PlatformJanus, "")
	if err != nil {
		t.Fatalf("Janus Recommend: unexpected error: %v", err)
	}
	if p.PreferredCodec != audio.CodecOpus {
		t.Errorf("Janus: PreferredCodec = %q, want opus", p.PreferredCodec)
	}
	if !p.AGCRecommended {
		t.Errorf("Janus: AGCRecommended should be true for WebRTC paths")
	}
	if p.SampleRate != 48000 {
		t.Errorf("Janus: SampleRate = %d, want 48000", p.SampleRate)
	}
}

// TestRecommendFreeSWITCH checks FreeSWITCH PCMU preference and ESL path.
func TestRecommendFreeSWITCH(t *testing.T) {
	cases := []struct {
		version          string
		wantPathContains string
		wantErr          bool
	}{
		{"20.26.2", "mod_audio_stream", false},
		{"1.10.0", "mod_audio_stream", false},
		{"1.8.0", "", true},        // below minimum (1.10.0 is min)
		{"", "mod_spandsp", false}, // no version → generic advice
	}
	for _, tc := range cases {
		p, err := compat.Recommend(compat.PlatformFreeSWITCH, tc.version)
		if tc.wantErr {
			if err == nil {
				t.Errorf("FreeSWITCH %q: expected error", tc.version)
			}
			continue
		}
		if err != nil {
			t.Errorf("FreeSWITCH %q: unexpected error: %v", tc.version, err)
			continue
		}
		if !strings.Contains(p.IntegrationPath, tc.wantPathContains) {
			t.Errorf("FreeSWITCH %q: IntegrationPath %q missing %q",
				tc.version, p.IntegrationPath, tc.wantPathContains)
		}
		if p.PreferredCodec != audio.CodecG711U {
			t.Errorf("FreeSWITCH %q: PreferredCodec = %q, want pcm_mulaw", tc.version, p.PreferredCodec)
		}
	}
}

// TestRecommendKamailioRTPEngine verifies Kamailio/RTPEngine profile.
func TestRecommendKamailioRTPEngine(t *testing.T) {
	for _, platform := range []compat.Platform{compat.PlatformKamailio, compat.PlatformRTPEngine} {
		p, err := compat.Recommend(platform, "11.5.1")
		if err != nil {
			t.Fatalf("%s Recommend: unexpected error: %v", platform, err)
		}
		if !strings.Contains(p.IntegrationPath, "NG control protocol") {
			t.Errorf("%s: IntegrationPath %q missing NG control protocol", platform, p.IntegrationPath)
		}
		if p.PreferredCodec != audio.CodecG711A {
			t.Errorf("%s: PreferredCodec = %q, want pcm_alaw", platform, p.PreferredCodec)
		}
		// rtpengine below minimum should error
		_, err = compat.Recommend(platform, "10.9.9")
		if err == nil {
			t.Errorf("%s 10.9.9: expected error for below-minimum version", platform)
		}
	}
}

// TestRecommendGenericWSS verifies generic WSS profile.
func TestRecommendGenericWSS(t *testing.T) {
	p, err := compat.Recommend(compat.PlatformGenericWSS, "")
	if err != nil {
		t.Fatalf("GenericWSS Recommend: %v", err)
	}
	if p.PreferredCodec != audio.CodecOpus {
		t.Errorf("GenericWSS: PreferredCodec = %q, want opus", p.PreferredCodec)
	}
	if !p.AGCRecommended {
		t.Error("GenericWSS: AGCRecommended should be true")
	}
}

// TestRecommendGenericRTP verifies generic RTP profile.
func TestRecommendGenericRTP(t *testing.T) {
	p, err := compat.Recommend(compat.PlatformGenericRTP, "")
	if err != nil {
		t.Fatalf("GenericRTP Recommend: %v", err)
	}
	if p.PreferredCodec != audio.CodecG711U {
		t.Errorf("GenericRTP: PreferredCodec = %q, want pcm_mulaw", p.PreferredCodec)
	}
	if !strings.Contains(p.IntegrationPath, "RTP proxy") {
		t.Errorf("GenericRTP: IntegrationPath %q should mention RTP proxy", p.IntegrationPath)
	}
}

// TestRecommendUnknownPlatform verifies an error is returned for unknown platforms.
func TestRecommendUnknownPlatform(t *testing.T) {
	_, err := compat.Recommend("unknownplatform", "1.0.0")
	if err == nil {
		t.Error("expected error for unknown platform, got nil")
	}
}

// TestProfileSummary checks that Summary() returns a non-empty, formatted string.
func TestProfileSummary(t *testing.T) {
	p, err := compat.Recommend(compat.PlatformVSIP, "")
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	s := p.Summary()
	if s == "" {
		t.Error("Summary() returned empty string")
	}
	if !strings.Contains(s, "vsip") {
		t.Errorf("Summary() %q does not contain platform name", s)
	}
	if !strings.Contains(s, "path=") {
		t.Errorf("Summary() %q missing path= field", s)
	}
}

// TestMinSupportedVersionConstants verifies the exported minimum version constants are sane.
func TestMinSupportedVersionConstants(t *testing.T) {
	if !compat.AsteriskMinSupported.GTE(compat.ParseVersion("16.0.0")) {
		t.Errorf("AsteriskMinSupported should be >= 16.0.0, got %s", compat.AsteriskMinSupported)
	}
	if !compat.FreeSWITCHMinSupported.GTE(compat.ParseVersion("1.10.0")) {
		t.Errorf("FreeSWITCHMinSupported should be >= 1.10.0, got %s", compat.FreeSWITCHMinSupported)
	}
	if !compat.RTPEngineMinSupported.GTE(compat.ParseVersion("11.0.0")) {
		t.Errorf("RTPEngineMinSupported should be >= 11.0.0, got %s", compat.RTPEngineMinSupported)
	}
}
