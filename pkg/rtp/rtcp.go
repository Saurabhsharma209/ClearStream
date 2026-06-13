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

// ParseRTCPReceiverReport parses a raw RTCP packet.
// Returns nil if the packet is not a Receiver Report.
func ParseRTCPReceiverReport(data []byte) (*RTCPReceiverReport, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("rtcp: packet too short (%d bytes)", len(data))
	}

	// Byte 0: version (2 bits) + padding (1 bit) + RC (5 bits)
	// version must be 2
	version := (data[0] >> 6) & 0x3
	if version != 2 {
		return nil, fmt.Errorf("rtcp: invalid version %d", version)
	}

	rc := data[0] & 0x1F // report count
	pt := RTCPPacketType(data[1])

	if pt != RTCPTypeReceiverReport {
		return nil, nil // not a RR, silently ignore
	}
	if rc == 0 {
		return nil, nil // no report blocks
	}

	// Sender SSRC (bytes 4–7)
	// First report block starts at byte 8
	if len(data) < 32 { // 8 header + 24 report block
		return nil, fmt.Errorf("rtcp: RR too short")
	}

	rr := &RTCPReceiverReport{}
	rr.SSRC = binary.BigEndian.Uint32(data[8:12])
	rr.FractionLost = float64(data[12]) / 256.0

	// Cumulative lost is 24-bit signed
	lost := int32(data[13])<<16 | int32(data[14])<<8 | int32(data[15])
	if lost&0x800000 != 0 {
		lost |= ^int32(0xFFFFFF) // sign extend
	}
	rr.CumulativeLost = lost

	rr.HighestSeq = binary.BigEndian.Uint32(data[16:20])
	rr.Jitter = binary.BigEndian.Uint32(data[20:24])
	rr.LastSR = binary.BigEndian.Uint32(data[24:28])
	rr.DelaySinceLastSR = binary.BigEndian.Uint32(data[28:32])

	return rr, nil
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
