package sip

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
)

// TestSDPG722BandMode verifies that G.722 SDP (which declares clock=8000 per
// RFC 3551) is correctly identified as wideband (16 kHz), not narrowband.
// This is the critical RFC 3551 quirk: G722/8000 in SDP means 16kHz audio.
func TestSDPG722BandMode(t *testing.T) {
	sdp := "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=ClearStream Test\r\n" +
		"c=IN IP4 127.0.0.1\r\n" +
		"t=0 0\r\n" +
		"m=audio 5004 RTP/AVP 9\r\n" +
		"a=rtpmap:9 G722/8000\r\n" +
		"a=ptime:20\r\n"

	m := ParseSDP(sdp)

	if m.Codec != audio.CodecG722 {
		t.Errorf("codec = %q, want %q", m.Codec, audio.CodecG722)
	}

	got := m.BandMode()
	if got != audio.BandWide {
		t.Errorf("BandMode() = %v, want BandWide (16kHz); "+
			"G722/8000 in SDP is RFC 3551 quirk — real audio is 16kHz wideband", got)
	}
}

// TestSDPPCMUBandMode verifies that PCMU/8000 is correctly identified as narrowband.
func TestSDPPCMUBandMode(t *testing.T) {
	sdp := "v=0\r\n" +
		"o=- 0 0 IN IP4 127.0.0.1\r\n" +
		"s=ClearStream Test\r\n" +
		"c=IN IP4 127.0.0.1\r\n" +
		"t=0 0\r\n" +
		"m=audio 5004 RTP/AVP 0\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=ptime:20\r\n"

	m := ParseSDP(sdp)

	if m.Codec != audio.CodecG711U {
		t.Errorf("codec = %q, want %q", m.Codec, audio.CodecG711U)
	}

	got := m.BandMode()
	if got != audio.BandNarrow {
		t.Errorf("BandMode() = %v, want BandNarrow (8kHz) for PCMU", got)
	}
}
