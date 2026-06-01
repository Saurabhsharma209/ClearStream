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
