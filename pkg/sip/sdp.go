// Package sip provides SIP-aware media proxy capabilities for ClearStream.
package sip

import (
	"fmt"
	"strings"

	"github.com/exotel/clearstream/pkg/audio"
)

// SDPMedia holds parsed media info from a SIP SDP body.
type SDPMedia struct {
	Port        int
	Codec       audio.Codec
	PayloadType uint8
	SampleRate  int
	Ptime       int // packet time in ms (default 20)
}

// ParseSDP extracts media parameters from a SIP SDP body string.
// Handles the common cases: PCMU (0), PCMA (8), G722 (9), G729 (18), Opus (dynamic).
func ParseSDP(sdp string) SDPMedia {
	m := SDPMedia{
		Port:        5004,
		Codec:       audio.CodecG711U,
		PayloadType: 0,
		SampleRate:  8000,
		Ptime:       20,
	}
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "m=audio"):
			// m=audio 5004 RTP/AVP 0 8 18
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var port int
				if _, err := fmt.Sscanf(parts[1], "%d", &port); err == nil {
					m.Port = port
				}
			}
			// First payload type determines codec
			if len(parts) >= 4 {
				var pt uint8
				if _, err := fmt.Sscanf(parts[3], "%d", &pt); err == nil {
					m.PayloadType = pt
					m.Codec = payloadTypeToCodec(pt)
					m.SampleRate = m.Codec.NativeSampleRate()
				}
			}
		case strings.HasPrefix(line, "a=ptime:"):
			fmt.Sscanf(strings.TrimPrefix(line, "a=ptime:"), "%d", &m.Ptime)
		case strings.HasPrefix(line, "a=rtpmap:"):
			// a=rtpmap:111 opus/48000/2
			var pt uint8
			var name string
			var rate int
			if n, _ := fmt.Sscanf(strings.TrimPrefix(line, "a=rtpmap:"), "%d %s", &pt, &name); n == 2 {
				parts := strings.Split(name, "/")
				if len(parts) >= 2 {
					fmt.Sscanf(parts[1], "%d", &rate)
				}
				if pt == m.PayloadType {
					m.Codec = normalizeSDPCodec(parts[0])
					if rate > 0 {
						m.SampleRate = rate
					}
				}
			}
		}
	}
	return m
}

func normalizeSDPCodec(name string) audio.Codec {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "pcmu":
		return audio.CodecG711U
	case "pcma":
		return audio.CodecG711A
	case "g722":
		return audio.CodecG722
	case "g729":
		return audio.CodecG729
	case "opus":
		return audio.CodecOpus
	default:
		return audio.CodecG711U
	}
}

func payloadTypeToCodec(pt uint8) audio.Codec {
	switch pt {
	case 0:
		return audio.CodecG711U
	case 8:
		return audio.CodecG711A
	case 9:
		return audio.CodecG722
	case 18:
		return audio.CodecG729
	default:
		return audio.CodecG711U
	}
}

// BandMode returns the BandMode for the codec in this SDPMedia.
// It correctly handles the G.722 RFC 3551 clock-rate quirk: SDP declares
// G722/8000, but the actual audio is 16 kHz wideband — so we return
// BandWide, not BandNarrow.
func (m *SDPMedia) BandMode() audio.BandMode {
	switch m.Codec {
	case audio.CodecG711U, audio.CodecG711A, audio.CodecG729, audio.CodecGSM, audio.CodecILBC:
		return audio.BandNarrow
	case audio.CodecG722, audio.CodecSpeex:
		return audio.BandWide // G.722 RFC 3551: SDP clock=8000 but real audio is 16kHz
	case audio.CodecOpus:
		return audio.BandFull
	default:
		return audio.BandNarrow // safe default for Indian PSTN
	}
}
