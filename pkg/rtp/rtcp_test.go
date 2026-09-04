package rtp

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestParseRTCPReceiverReport(t *testing.T) {
	// Craft a minimal RTCP RR packet
	// V=2, P=0, RC=1, PT=201, length=7
	pkt := []byte{
		0x81, 0xC9, 0x00, 0x07, // header: V=2,RC=1,PT=201,len=7
		0x00, 0x00, 0x00, 0x01, // sender SSRC
		// Report block:
		0xDE, 0xAD, 0xBE, 0xEF, // SSRC of source
		0x80, 0x00, 0x00, 0x05, // fraction_lost=128/256=0.5, cumulative=5
		0x00, 0x01, 0x00, 0x00, // extended highest seq
		0x00, 0x00, 0x00, 0x0A, // jitter = 10
		0x00, 0x00, 0x00, 0x00, // last SR
		0x00, 0x00, 0x00, 0x00, // delay since last SR
	}

	rr, err := ParseRTCPReceiverReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if rr == nil {
		t.Fatal("expected RR, got nil")
	}

	if math.Abs(rr.FractionLost-0.5) > 0.01 {
		t.Errorf("fraction lost: got %.2f, want 0.50", rr.FractionLost)
	}
	if rr.CumulativeLost != 5 {
		t.Errorf("cumulative lost: got %d, want 5", rr.CumulativeLost)
	}
	if rr.Jitter != 10 {
		t.Errorf("jitter: got %d, want 10", rr.Jitter)
	}
}

func TestParseRTCPTooShort(t *testing.T) {
	_, err := ParseRTCPReceiverReport([]byte{0x81, 0xC9})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestParseRTCPWrongType(t *testing.T) {
	// PT=200 (Sender Report) — should return nil, nil
	pkt := make([]byte, 32)
	pkt[0] = 0x81
	pkt[1] = 0xC8 // PT=200 SR
	rr, err := ParseRTCPReceiverReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if rr != nil {
		t.Error("expected nil for SR packet")
	}
}

func TestPLCFadeToSilence_RTCPBasic(t *testing.T) {
	jb := NewJitterBuffer(4)

	// Seed lastGoodFrame directly via onGoodPacket (simulates a received frame)
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = 10000
	}
	jb.onGoodPacket(frame)

	// Loss 1-2 use pitch-period waveform substitution (see GeneratePLC in
	// jitter.go): both calls repeat the same detected pitch period from the
	// same lastGoodFrame, so they are non-decaying relative to each other.
	// Loss 3+ is where the exponential fade-to-silence actually begins.
	plc1 := jb.generatePLC()
	plc2 := jb.generatePLC()
	plc3 := jb.generatePLC()

	energy := func(f []int16) float64 {
		var sum float64
		for _, s := range f {
			sum += float64(s) * float64(s)
		}
		return sum
	}

	if energy(plc1) == 0 {
		t.Error("PLC frame 1 should be non-silent (pitch-period waveform substitution)")
	}
	if energy(plc2) > energy(plc1) {
		t.Error("PLC frame 2 should not be louder than frame 1 (still in substitution phase)")
	}
	if energy(plc2) <= energy(plc3) {
		t.Error("PLC frame 2 should be louder than frame 3 (fade to silence begins at loss 3)")
	}
}

func TestParseRTCPSenderReport(t *testing.T) {
	// Craft a minimal RTCP SR packet (28 bytes, no report blocks)
	// V=2, P=0, RC=0, PT=200, length=6 (6+1 * 4 = 28 bytes)
	pkt := []byte{
		0x80, 0xC8, 0x00, 0x06, // header: V=2,P=0,RC=0,PT=200,len=6
		0x00, 0x00, 0x00, 0x02, // sender SSRC = 2
		0xE6, 0x01, 0x23, 0x45, // NTP MSW
		0xAB, 0xCD, 0xEF, 0x00, // NTP LSW
		0x00, 0x00, 0x03, 0xE8, // RTP timestamp = 1000
		0x00, 0x00, 0x00, 0x64, // packet count = 100
		0x00, 0x00, 0x28, 0x00, // octet count = 10240
	}

	sr, err := ParseRTCPSenderReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if sr == nil {
		t.Fatal("expected SR, got nil")
	}

	if sr.SSRC != 2 {
		t.Errorf("SSRC: got %d, want 2", sr.SSRC)
	}
	if sr.NTPSec != 0xE6012345 {
		t.Errorf("NTPSec: got 0x%08X, want 0xE6012345", sr.NTPSec)
	}
	if sr.NTPFrac != 0xABCDEF00 {
		t.Errorf("NTPFrac: got 0x%08X, want 0xABCDEF00", sr.NTPFrac)
	}
	if sr.RTPTimestamp != 1000 {
		t.Errorf("RTPTimestamp: got %d, want 1000", sr.RTPTimestamp)
	}
	if sr.PacketCount != 100 {
		t.Errorf("PacketCount: got %d, want 100", sr.PacketCount)
	}
	if sr.OctetCount != 10240 {
		t.Errorf("OctetCount: got %d, want 10240", sr.OctetCount)
	}
}

func TestParseRTCPSRTooShort(t *testing.T) {
	// Only 20 bytes — shorter than the 28-byte SR minimum
	pkt := make([]byte, 20)
	pkt[0] = 0x80 // V=2, P=0, RC=0
	pkt[1] = 0xC8 // PT=200 SR
	_, err := ParseRTCPSenderReport(pkt)
	if err == nil {
		t.Error("expected error for SR packet shorter than 28 bytes")
	}
}

func TestParseRTCPSRWrongType(t *testing.T) {
	// PT=201 (RR) passed to SR parser — should return nil, nil
	pkt := make([]byte, 32)
	pkt[0] = 0x81 // V=2, RC=1
	pkt[1] = 0xC9 // PT=201 RR
	sr, err := ParseRTCPSenderReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if sr != nil {
		t.Error("expected nil for non-SR packet")
	}
}

// TestParseRTCPReceiverReportBlocks_MultipleBlocks verifies that a compound
// RR packet with RC=2 (two reception report blocks, e.g. a mixer/bridge
// reporting on two different source SSRCs in one packet) yields both blocks
// intact instead of the previous behavior of silently keeping only the
// first.
func TestParseRTCPReceiverReportBlocks_MultipleBlocks(t *testing.T) {
	pkt := make([]byte, 8+2*24)
	pkt[0] = 0x82 // V=2, P=0, RC=2
	pkt[1] = 0xC9 // PT=201 RR
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)/4-1))
	binary.BigEndian.PutUint32(pkt[4:8], 0xCAFEBABE) // sender SSRC

	// Block 0: SSRC=0x11111111, jitter=111
	binary.BigEndian.PutUint32(pkt[8:12], 0x11111111)
	binary.BigEndian.PutUint32(pkt[20:24], 111)

	// Block 1: SSRC=0x22222222, jitter=222
	binary.BigEndian.PutUint32(pkt[32:36], 0x22222222)
	binary.BigEndian.PutUint32(pkt[44:48], 222)

	blocks, err := ParseRTCPReceiverReportBlocks(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 report blocks, got %d", len(blocks))
	}
	if blocks[0].SSRC != 0x11111111 || blocks[0].Jitter != 111 {
		t.Errorf("block 0: got SSRC=%08X Jitter=%d, want SSRC=11111111 Jitter=111", blocks[0].SSRC, blocks[0].Jitter)
	}
	if blocks[1].SSRC != 0x22222222 || blocks[1].Jitter != 222 {
		t.Errorf("block 1: got SSRC=%08X Jitter=%d, want SSRC=22222222 Jitter=222", blocks[1].SSRC, blocks[1].Jitter)
	}

	// ParseRTCPReceiverReport (singular) must still return the first block,
	// preserving backward compatibility for existing callers.
	first, err := ParseRTCPReceiverReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.SSRC != 0x11111111 {
		t.Fatalf("ParseRTCPReceiverReport should still return the first block")
	}
}

// TestParseRTCPReportBlocks_SenderReportWithEmbeddedBlocks verifies that
// ParseRTCPReportBlocks extracts reception report blocks carried inside an
// RTCP Sender Report (PT=200), not just plain Receiver Reports (PT=201).
// This is the common real-world case: an endpoint that is both sending and
// receiving audio (true for essentially every two-way SIP call) reports its
// reception statistics via SR, embedding the same 24-byte report-block
// format after its 20-byte sender-info section. The RR-only
// ParseRTCPReceiverReportBlocks would silently return nothing for this
// packet even though it carries real loss/jitter data.
func TestParseRTCPReportBlocks_SenderReportWithEmbeddedBlocks(t *testing.T) {
	pkt := make([]byte, 28+24)
	pkt[0] = 0x81 // V=2, P=0, RC=1
	pkt[1] = 0xC8 // PT=200 SR
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)/4-1))
	binary.BigEndian.PutUint32(pkt[4:8], 0xAAAAAAAA) // sender SSRC
	// Sender info (NTP MSW/LSW, RTP TS, packet count, octet count) — values
	// don't matter for this test, leave zeroed.

	// Embedded reception report block, starting at byte 28.
	binary.BigEndian.PutUint32(pkt[28:32], 0x33333333) // SSRC of source
	pkt[32] = 0x40                                     // fraction lost = 64/256 = 0.25
	binary.BigEndian.PutUint32(pkt[40:44], 77)         // jitter = 77

	blocks, err := ParseRTCPReportBlocks(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 embedded report block from SR, got %d", len(blocks))
	}
	if blocks[0].SSRC != 0x33333333 {
		t.Errorf("SSRC: got %08X, want 33333333", blocks[0].SSRC)
	}
	if blocks[0].Jitter != 77 {
		t.Errorf("jitter: got %d, want 77", blocks[0].Jitter)
	}
	if math.Abs(blocks[0].FractionLost-0.25) > 0.01 {
		t.Errorf("fraction lost: got %.2f, want 0.25", blocks[0].FractionLost)
	}
}

// TestParseRTCPReportBlocks_PlainReceiverReport verifies ParseRTCPReportBlocks
// still handles ordinary RR packets (PT=201) identically to
// ParseRTCPReceiverReportBlocks.
func TestParseRTCPReportBlocks_PlainReceiverReport(t *testing.T) {
	pkt := make([]byte, 8+24)
	pkt[0] = 0x81 // V=2, RC=1
	pkt[1] = 0xC9 // PT=201 RR
	binary.BigEndian.PutUint32(pkt[8:12], 0x44444444)
	binary.BigEndian.PutUint32(pkt[20:24], 55)

	blocks, err := ParseRTCPReportBlocks(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].SSRC != 0x44444444 || blocks[0].Jitter != 55 {
		t.Fatalf("unexpected result: %+v", blocks)
	}
}

// TestParseRTCPReportBlocks_SenderReportNoBlocks verifies a bare SR (RC=0,
// no embedded reception blocks -- the common case for a one-way media leg
// that has nothing to report on yet) returns (nil, nil) rather than an error.
func TestParseRTCPReportBlocks_SenderReportNoBlocks(t *testing.T) {
	pkt := make([]byte, 28)
	pkt[0] = 0x80 // V=2, RC=0
	pkt[1] = 0xC8 // PT=200 SR

	blocks, err := ParseRTCPReportBlocks(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if blocks != nil {
		t.Errorf("expected nil blocks for RC=0 SR, got %+v", blocks)
	}
}

// TestParseRTCPReportBlocks_TruncatedSR verifies an SR that declares RC=1 but
// doesn't actually carry the 24 bytes for that block is rejected as
// malformed, matching ParseRTCPReceiverReportBlocks' truncation handling.
func TestParseRTCPReportBlocks_TruncatedSR(t *testing.T) {
	pkt := make([]byte, 28) // SR fixed header only, no room for the claimed block
	pkt[0] = 0x81           // V=2, RC=1
	pkt[1] = 0xC8           // PT=200 SR

	_, err := ParseRTCPReportBlocks(pkt)
	if err == nil {
		t.Fatal("expected error for SR declaring RC=1 with no block data")
	}
}

// TestParseRTCPReceiverReportBlocks_TruncatedMultiBlock verifies that a
// packet declaring RC=2 but only carrying one block's worth of data is
// rejected as malformed rather than silently parsed from out-of-bounds/wrong
// data.
func TestParseRTCPReceiverReportBlocks_TruncatedMultiBlock(t *testing.T) {
	pkt := make([]byte, 8+24) // only 1 block's worth of data
	pkt[0] = 0x82             // V=2, P=0, RC=2 (claims 2 blocks)
	pkt[1] = 0xC9             // PT=201 RR

	_, err := ParseRTCPReceiverReportBlocks(pkt)
	if err == nil {
		t.Fatal("expected error for RC=2 packet with only 1 block's worth of data")
	}
}
