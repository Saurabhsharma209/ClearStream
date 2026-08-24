package rtp

import (
	"encoding/binary"
	"testing"
)

// TestParseRTPHeaderExtensionExactlyFillsPacket is a regression test for an
// off-by-one boundary bug: a packet whose header extension exactly fills the
// remainder of the packet (zero-length extension, no payload after it) is a
// legal RFC 3550 5.3.1 packet, but the old guard (len(raw) > offset+4)
// rejected it, leaving the extension header bytes in the returned payload.
func TestParseRTPHeaderExtensionExactlyFillsPacket(t *testing.T) {
	buf := make([]byte, 16)
	buf[0] = 0x90
	binary.BigEndian.PutUint16(buf[2:4], 1)
	binary.BigEndian.PutUint32(buf[4:8], 100)
	binary.BigEndian.PutUint32(buf[8:12], 0xDEAD)
	binary.BigEndian.PutUint16(buf[12:14], 0xBEDE)
	binary.BigEndian.PutUint16(buf[14:16], 0)

	h, payload, err := parseRTPHeader(buf)
	if err != nil {
		t.Fatalf("parseRTPHeader: unexpected error: %v", err)
	}
	if !h.Extension {
		t.Fatal("Extension bit should be set")
	}
	if len(payload) != 0 {
		t.Fatalf("expected empty payload, got %d bytes: %x", len(payload), payload)
	}
}
