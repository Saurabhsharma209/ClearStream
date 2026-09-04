// Package rtp RTCP support — parse Receiver Reports for quality monitoring.
package rtp

import (
	"encoding/binary"
	"fmt"
)

// RTCPPacketType identifies the RTCP packet type.
type RTCPPacketType uint8

const (
	RTCPTypeReceiverReport RTCPPacketType = 201
	RTCPTypeSenderReport   RTCPPacketType = 200
)

// RTCPReceiverReport holds quality statistics from an RTCP Receiver Report.
type RTCPReceiverReport struct {
	SSRC             uint32
	FractionLost     float64 // 0.0–1.0
	CumulativeLost   int32
	HighestSeq       uint32
	Jitter           uint32 // in RTP timestamp units
	LastSR           uint32
	DelaySinceLastSR uint32
}

// ParseRTCPReceiverReportBlocks parses every reception report block carried
// by an RTCP Receiver Report packet (RFC 3550 §6.4.2).
//
// A single RR packet can report on multiple sources at once: the RC field in
// the packet header (bits 0-4 of byte 0) gives the number of 24-byte report
// blocks that follow the 8-byte common+SSRC header, and RFC 3550 allows up to
// 31 of them in one packet (e.g. a conference bridge/mixer reporting on every
// leg it is receiving, in a single compound RTCP datagram). The previous
// implementation only ever read the first block and silently discarded the
// rest, which is fine for simple 1:1 calls but wrong the moment more than one
// block is present -- whichever source happens to sort first in the packet
// would silently win, even if it isn't the SSRC this session actually cares
// about, and every other source's loss/jitter numbers were dropped on the
// floor with no way for a caller to ever see them.
//
// Returns one RTCPReceiverReport per block, in on-wire order. Returns
// (nil, nil) if the packet is not a Receiver Report (PT != 201) or carries
// zero report blocks (RC == 0) -- callers should treat that the same as "no
// data available", not an error.
func ParseRTCPReceiverReportBlocks(data []byte) ([]RTCPReceiverReport, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("rtcp: packet too short (%d bytes)", len(data))
	}

	// Byte 0: version (2 bits) + padding (1 bit) + RC (5 bits)
	// version must be 2
	version := (data[0] >> 6) & 0x3
	if version != 2 {
		return nil, fmt.Errorf("rtcp: invalid version %d", version)
	}

	rc := int(data[0] & 0x1F) // report count
	pt := RTCPPacketType(data[1])

	if pt != RTCPTypeReceiverReport {
		return nil, nil // not a RR, silently ignore
	}
	if rc == 0 {
		return nil, nil // no report blocks
	}

	// Sender SSRC (bytes 4–7); report blocks start at byte 8, 24 bytes each.
	need := 8 + rc*24
	if len(data) < need {
		return nil, fmt.Errorf("rtcp: RR too short for %d report block(s): have %d bytes, need %d", rc, len(data), need)
	}

	reports := make([]RTCPReceiverReport, 0, rc)
	for i := 0; i < rc; i++ {
		off := 8 + i*24

		rr := RTCPReceiverReport{}
		rr.SSRC = binary.BigEndian.Uint32(data[off : off+4])
		rr.FractionLost = float64(data[off+4]) / 256.0

		// Cumulative lost is 24-bit signed
		lost := int32(data[off+5])<<16 | int32(data[off+6])<<8 | int32(data[off+7])
		if lost&0x800000 != 0 {
			lost |= ^int32(0xFFFFFF) // sign extend
		}
		rr.CumulativeLost = lost

		rr.HighestSeq = binary.BigEndian.Uint32(data[off+8 : off+12])
		rr.Jitter = binary.BigEndian.Uint32(data[off+12 : off+16])
		rr.LastSR = binary.BigEndian.Uint32(data[off+16 : off+20])
		rr.DelaySinceLastSR = binary.BigEndian.Uint32(data[off+20 : off+24])

		reports = append(reports, rr)
	}

	return reports, nil
}

// ParseRTCPReceiverReport parses a raw RTCP packet and returns its first
// reception report block, preserving the original single-block API.
// Returns nil if the packet is not a Receiver Report, or carries no report
// blocks.
//
// Prefer ParseRTCPReceiverReportBlocks (and select the block matching the
// SSRC you actually care about) whenever the peer might report on more than
// one source in the same packet -- this function silently keeps only
// whichever block sorts first on the wire.
func ParseRTCPReceiverReport(data []byte) (*RTCPReceiverReport, error) {
	blocks, err := ParseRTCPReceiverReportBlocks(data)
	if err != nil || len(blocks) == 0 {
		return nil, err
	}
	first := blocks[0]
	return &first, nil
}

// ParseRTCPReportBlocks parses the reception report blocks carried by either
// an RTCP Sender Report (PT=200) or Receiver Report (PT=201) packet (RFC 3550
// SS6.4.1/6.4.2). Both packet types share an identical 24-byte report-block
// format; they differ only in where the blocks start -- an SR has a 20-byte
// sender-info section between the 4-byte SSRC and the report blocks that an
// RR doesn't.
//
// This matters because ParseRTCPReceiverReportBlocks only recognizes PT=201
// packets. In practice, an endpoint that is both sending and receiving audio
// (true for essentially every two-way SIP call) reports its own reception
// statistics via SR, not RR -- pure RR packets are only sent by receive-only
// participants. A session that only inspected PT=201 packets would silently
// see zero loss/jitter data for the entire call from any peer that reports
// via SR-with-embedded-blocks (the common case), even though that peer sends
// real reception report blocks every few seconds.
//
// Returns (nil, nil) if the packet is neither an SR nor an RR, or carries
// zero report blocks (RC == 0).
func ParseRTCPReportBlocks(data []byte) ([]RTCPReceiverReport, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("rtcp: packet too short (%d bytes)", len(data))
	}

	version := (data[0] >> 6) & 0x3
	if version != 2 {
		return nil, fmt.Errorf("rtcp: invalid version %d", version)
	}

	rc := int(data[0] & 0x1F)
	pt := RTCPPacketType(data[1])

	var base int
	switch pt {
	case RTCPTypeReceiverReport:
		base = 8 // 4-byte common header + 4-byte SSRC
	case RTCPTypeSenderReport:
		base = 28 // 4-byte common header + 4-byte SSRC + 20-byte sender info
	default:
		return nil, nil // neither SR nor RR, silently ignore
	}
	if rc == 0 {
		return nil, nil // no report blocks
	}

	need := base + rc*24
	if len(data) < need {
		return nil, fmt.Errorf("rtcp: packet too short for %d report block(s): have %d bytes, need %d", rc, len(data), need)
	}

	reports := make([]RTCPReceiverReport, 0, rc)
	for i := 0; i < rc; i++ {
		off := base + i*24

		rr := RTCPReceiverReport{}
		rr.SSRC = binary.BigEndian.Uint32(data[off : off+4])
		rr.FractionLost = float64(data[off+4]) / 256.0

		lost := int32(data[off+5])<<16 | int32(data[off+6])<<8 | int32(data[off+7])
		if lost&0x800000 != 0 {
			lost |= ^int32(0xFFFFFF) // sign extend
		}
		rr.CumulativeLost = lost

		rr.HighestSeq = binary.BigEndian.Uint32(data[off+8 : off+12])
		rr.Jitter = binary.BigEndian.Uint32(data[off+12 : off+16])
		rr.LastSR = binary.BigEndian.Uint32(data[off+16 : off+20])
		rr.DelaySinceLastSR = binary.BigEndian.Uint32(data[off+20 : off+24])

		reports = append(reports, rr)
	}

	return reports, nil
}

// RTCPSenderReport holds the sender information fields from an RTCP SR packet.
// RFC 3550 §6.4.1
type RTCPSenderReport struct {
	SSRC         uint32
	NTPSec       uint32 // NTP timestamp, most significant word
	NTPFrac      uint32 // NTP timestamp, least significant word
	RTPTimestamp uint32
	PacketCount  uint32
	OctetCount   uint32
}

// ParseRTCPSenderReport parses a raw RTCP packet as a Sender Report.
// Returns nil if the packet is not a Sender Report (PT=200).
// The SR fixed header is 28 bytes: 4-byte common header + 4-byte sender SSRC +
// 20 bytes of sender info (NTP MSW, NTP LSW, RTP TS, packet count, octet count).
func ParseRTCPSenderReport(data []byte) (*RTCPSenderReport, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("rtcp: packet too short (%d bytes)", len(data))
	}

	version := (data[0] >> 6) & 0x3
	if version != 2 {
		return nil, fmt.Errorf("rtcp: invalid version %d", version)
	}

	pt := RTCPPacketType(data[1])
	if pt != RTCPTypeSenderReport {
		return nil, nil // not an SR, silently ignore
	}

	// SR fixed header requires 28 bytes minimum.
	if len(data) < 28 {
		return nil, fmt.Errorf("rtcp: SR too short (%d bytes, need 28)", len(data))
	}

	sr := &RTCPSenderReport{}
	sr.SSRC = binary.BigEndian.Uint32(data[4:8])
	sr.NTPSec = binary.BigEndian.Uint32(data[8:12])
	sr.NTPFrac = binary.BigEndian.Uint32(data[12:16])
	sr.RTPTimestamp = binary.BigEndian.Uint32(data[16:20])
	sr.PacketCount = binary.BigEndian.Uint32(data[20:24])
	sr.OctetCount = binary.BigEndian.Uint32(data[24:28])

	return sr, nil
}
