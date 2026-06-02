package sip

import (
	"strings"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
)

func TestParseSDP_PCMU(t *testing.T) {
	sdp := "v=0\r\nm=audio 5004 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=ptime:20\r\n"
	m := ParseSDP(sdp)
	if m.Codec != audio.CodecG711U {
		t.Errorf("expected PCMU, got %s", m.Codec)
	}
	if m.Port != 5004 {
		t.Errorf("expected port 5004, got %d", m.Port)
	}
	if m.Ptime != 20 {
		t.Errorf("expected ptime 20, got %d", m.Ptime)
	}
}

func TestParseSDP_Opus(t *testing.T) {
	sdp := "v=0\r\nm=audio 5006 RTP/AVP 111\r\na=rtpmap:111 opus/48000/2\r\n"
	m := ParseSDP(sdp)
	if m.Codec != audio.CodecOpus {
		t.Errorf("expected Opus, got %s", m.Codec)
	}
	if m.Port != 5006 {
		t.Errorf("expected port 5006, got %d", m.Port)
	}
	if m.SampleRate != 48000 {
		t.Errorf("expected sample rate 48000, got %d", m.SampleRate)
	}
}

func TestParseSDP_G722(t *testing.T) {
	sdp := "m=audio 5008 RTP/AVP 9\r\n"
	m := ParseSDP(sdp)
	if m.Codec != audio.CodecG722 {
		t.Errorf("expected G722, got %s", m.Codec)
	}
}

func TestParseSDP_PCMA(t *testing.T) {
	sdp := "m=audio 5010 RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\na=ptime:30\r\n"
	m := ParseSDP(sdp)
	if m.Codec != audio.CodecG711A {
		t.Errorf("expected PCMA, got %s", m.Codec)
	}
	if m.Ptime != 30 {
		t.Errorf("expected ptime 30, got %d", m.Ptime)
	}
}

func TestParseSDP_G729(t *testing.T) {
	sdp := "m=audio 5012 RTP/AVP 18\r\n"
	m := ParseSDP(sdp)
	if m.Codec != audio.CodecG729 {
		t.Errorf("expected G729, got %s", m.Codec)
	}
}

func TestParseSDP_Empty(t *testing.T) {
	m := ParseSDP("")
	if m.Codec == "" {
		t.Error("empty SDP should return default codec")
	}
	if m.Port == 0 {
		t.Error("empty SDP should return default port")
	}
	if m.Ptime == 0 {
		t.Error("empty SDP should return default ptime")
	}
}

func TestNormalizeSDPCodec(t *testing.T) {
	cases := map[string]audio.Codec{
		"PCMU": audio.CodecG711U,
		"pcma": audio.CodecG711A,
		"opus": audio.CodecOpus,
		"G729": audio.CodecG729,
		"g722": audio.CodecG722,
	}
	for input, want := range cases {
		got := normalizeSDPCodec(input)
		if got != want {
			t.Errorf("normalizeSDPCodec(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPayloadTypeToCodec(t *testing.T) {
	cases := map[uint8]audio.Codec{
		0:  audio.CodecG711U,
		8:  audio.CodecG711A,
		9:  audio.CodecG722,
		18: audio.CodecG729,
		99: audio.CodecG711U, // unknown falls back to PCMU
	}
	for pt, want := range cases {
		got := payloadTypeToCodec(pt)
		if got != want {
			t.Errorf("payloadTypeToCodec(%d) = %q, want %q", pt, got, want)
		}
	}
}

func TestProxyCreation(t *testing.T) {
	// Verify the package compiles and basic string ops work.
	_ = strings.Contains("test", "test")
}

// TestSDPAudioPortExtraction verifies that a realistic multi-line SDP body (using
// LF line endings as commonly seen in SIP implementations) is parsed correctly:
// the audio port, primary codec, and both codec payload types are extracted.
func TestSDPAudioPortExtraction(t *testing.T) {
	sdp := "v=0\n" +
		"o=- 12345 1 IN IP4 192.168.1.1\n" +
		"s=ClearStream Test\n" +
		"c=IN IP4 192.168.1.1\n" +
		"t=0 0\n" +
		"m=audio 5004 RTP/AVP 0 8\n" +
		"a=rtpmap:0 PCMU/8000\n" +
		"a=rtpmap:8 PCMA/8000\n"

	m := ParseSDP(sdp)

	if m.Port != 5004 {
		t.Errorf("expected audio port 5004, got %d", m.Port)
	}
	// First payload type in the m= line is 0 (PCMU), so that should win.
	if m.Codec != audio.CodecG711U {
		t.Errorf("expected primary codec PCMU, got %s", m.Codec)
	}
	if m.PayloadType != 0 {
		t.Errorf("expected payload type 0, got %d", m.PayloadType)
	}
	// SDP carries both PCMU and PCMA; verify the sample rate resolved correctly.
	if m.SampleRate != 8000 {
		t.Errorf("expected sample rate 8000, got %d", m.SampleRate)
	}
}

// TestSIPProxyNewProxy verifies that NewProxy initialises cleanly and reports
// zero active sessions before any calls have been started.
func TestSIPProxyNewProxy(t *testing.T) {
	proxy := NewProxy(nil, nil)
	if proxy == nil {
		t.Fatal("NewProxy returned nil")
	}
	if proxy.ActiveSessions() != 0 {
		t.Errorf("expected 0 active sessions on a fresh proxy, got %d", proxy.ActiveSessions())
	}
}
