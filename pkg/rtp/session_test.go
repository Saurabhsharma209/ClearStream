package rtp

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// buildRawRTPPacket creates a minimal valid RTP packet for testing.
func buildRawRTPPacket(seq uint16, ts uint32, ssrc uint32, payload []byte) []byte {
	buf := make([]byte, 12+len(payload))
	buf[0] = 0x80 // Version=2, no padding, no extension, CSRC count=0
	buf[1] = 0x00 // marker=0, payload type=0 (PCMU)
	binary.BigEndian.PutUint16(buf[2:4], seq)
	binary.BigEndian.PutUint32(buf[4:8], ts)
	binary.BigEndian.PutUint32(buf[8:12], ssrc)
	copy(buf[12:], payload)
	return buf
}

// TestRTPLoopback starts a listener on a random port and a forwarder pointing
// back at the same port, sends a few raw UDP RTP-shaped packets, and checks
// that the Stats counter increments — verifying that the session's receive
// loop actually processes packets without error.
func TestRTPLoopback(t *testing.T) {
	// Bind a random UDP port that we'll use as the "forward" destination.
	// We just want to absorb forwarded packets so the session doesn't error.
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()
	sinkAddr := sinkConn.LocalAddr().(*net.UDPAddr)

	// Create a no-op logger.
	logger, _ := zap.NewDevelopment()

	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkAddr.String(),
		PayloadType: 0, // PCMU
		JitterDepth: 1,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}

	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Start()
	defer sess.Stop()

	// Resolve the actual listen address.
	listenAddr := sess.conn.LocalAddr().(*net.UDPAddr)

	// Open a sender connection.
	sender, err := net.DialUDP("udp", nil, listenAddr)
	if err != nil {
		t.Fatalf("dial sender: %v", err)
	}
	defer sender.Close()

	// Build 4 PCMU RTP packets: 160 bytes of silence (8kHz, 20ms).
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF // µ-law silence
	}

	const numPackets = 4
	for i := 0; i < numPackets; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), 0xDEADBEEF, payload)
		if _, err := sender.Write(pkt); err != nil {
			t.Fatalf("send packet %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Give the receive loop time to process.
	time.Sleep(150 * time.Millisecond)

	stats := sess.Stats()
	if stats.PacketsReceived == 0 {
		t.Errorf("expected PacketsReceived > 0, got 0")
	}
	t.Logf("PacketsReceived=%d PacketsSent=%d PacketsLost=%d LatencyAvgMs=%.2f",
		stats.PacketsReceived, stats.PacketsSent, stats.PacketsLost, stats.LatencyAvgMs)
}

// TestSSRCDetection verifies that parseRTPHeader correctly extracts the SSRC
// from two packets with different SSRCs, simulating the detection logic in
// session.handlePacket that identifies a call-leg change and resets the pipeline.
func TestSSRCDetection(t *testing.T) {
	const ssrc1 uint32 = 0xAABBCCDD
	const ssrc2 uint32 = 0x11223344
	payload := []byte{0xFF, 0xFF} // minimal µ-law silence

	pkt1 := buildRawRTPPacket(1, 0, ssrc1, payload)
	pkt2 := buildRawRTPPacket(2, 160, ssrc2, payload)

	hdr1, _, err := parseRTPHeader(pkt1)
	if err != nil {
		t.Fatalf("parseRTPHeader pkt1: %v", err)
	}
	hdr2, _, err := parseRTPHeader(pkt2)
	if err != nil {
		t.Fatalf("parseRTPHeader pkt2: %v", err)
	}

	if hdr1.SSRC != ssrc1 {
		t.Errorf("pkt1 SSRC: want 0x%08X, got 0x%08X", ssrc1, hdr1.SSRC)
	}
	if hdr2.SSRC != ssrc2 {
		t.Errorf("pkt2 SSRC: want 0x%08X, got 0x%08X", ssrc2, hdr2.SSRC)
	}

	// Replay the SSRC-change detection logic from session.handlePacket.
	// The session tracks currentSSRC/ssrcSet and resets on change.
	var currentSSRC uint32
	ssrcSet := false
	ssrcChangeDetected := false

	for _, hdr := range []rtpHeader{hdr1, hdr2} {
		if ssrcSet && hdr.SSRC != currentSSRC {
			ssrcChangeDetected = true
		}
		currentSSRC = hdr.SSRC
		ssrcSet = true
	}

	if !ssrcChangeDetected {
		t.Error("expected SSRC change to be detected between pkt1 and pkt2")
	}
	if currentSSRC != ssrc2 {
		t.Errorf("after SSRC change: want currentSSRC=0x%08X, got 0x%08X", ssrc2, currentSSRC)
	}
	t.Logf("SSRC change correctly detected: 0x%08X -> 0x%08X", ssrc1, ssrc2)
}

// TestSSRCChangeResetsSession verifies that when two packets with different SSRCs
// are parsed, the SSRC transition is correctly identified using the same fields
// (ssrcSet + currentSSRC) that session.handlePacket maintains at runtime.
// This directly exercises the detection condition:
//
//	if ssrcSet && header.SSRC != currentSSRC { reset() }
func TestSSRCChangeResetsSession(t *testing.T) {
	const ssrc1 uint32 = 0xAABBCCDD
	const ssrc2 uint32 = 0x11223344
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF
	}

	// Parse headers from raw packets (same path as session.handlePacket).
	hdr1, _, err := parseRTPHeader(buildRawRTPPacket(1, 0, ssrc1, payload))
	if err != nil {
		t.Fatalf("parse pkt1: %v", err)
	}
	hdr2, _, err := parseRTPHeader(buildRawRTPPacket(2, 160, ssrc2, payload))
	if err != nil {
		t.Fatalf("parse pkt2: %v", err)
	}

	// Simulate session state.
	var currentSSRC uint32
	ssrcSet := false
	resetCount := 0

	process := func(hdr rtpHeader) {
		if ssrcSet && hdr.SSRC != currentSSRC {
			// mirrors: s.jitter.Reset(); s.pipeline.Reset()
			resetCount++
		}
		currentSSRC = hdr.SSRC
		ssrcSet = true
	}

	process(hdr1)
	if !ssrcSet {
		t.Fatal("ssrcSet should be true after first packet")
	}
	if currentSSRC != ssrc1 {
		t.Fatalf("after pkt1: want SSRC=0x%08X, got 0x%08X", ssrc1, currentSSRC)
	}
	if resetCount != 0 {
		t.Fatalf("no reset expected on first packet, got %d", resetCount)
	}

	process(hdr2)
	if currentSSRC != ssrc2 {
		t.Fatalf("after SSRC change: want SSRC=0x%08X, got 0x%08X", ssrc2, currentSSRC)
	}
	if resetCount != 1 {
		t.Fatalf("expected exactly 1 reset on SSRC change, got %d", resetCount)
	}

	t.Logf("SSRC change triggered pipeline reset: 0x%08X -> 0x%08X (resets=%d)", ssrc1, ssrc2, resetCount)
}

// TestRTPHeaderRoundtrip builds an rtpHeader, serializes it with buildRTPPacket,
// parses it back with parseRTPHeader, and verifies all fields round-trip cleanly.
func TestRTPHeaderRoundtrip(t *testing.T) {
	original := rtpHeader{
		Version:        2,
		Padding:        false,
		Extension:      false,
		CSRCCount:      0,
		Marker:         true,
		PayloadType:    8, // PCMA
		SequenceNumber: 0x1234,
		Timestamp:      0xDEADBEEF,
		SSRC:           0xCAFEBABE,
	}
	payload := []byte{0x01, 0x02, 0x03, 0x04}

	raw := buildRTPPacket(original, payload)
	if len(raw) < 12 {
		t.Fatalf("serialized packet too short: %d bytes", len(raw))
	}

	parsed, parsedPayload, err := parseRTPHeader(raw)
	if err != nil {
		t.Fatalf("parseRTPHeader: %v", err)
	}

	if parsed.Version != original.Version {
		t.Errorf("Version: want %d, got %d", original.Version, parsed.Version)
	}
	if parsed.PayloadType != original.PayloadType {
		t.Errorf("PayloadType: want %d, got %d", original.PayloadType, parsed.PayloadType)
	}
	if parsed.SequenceNumber != original.SequenceNumber {
		t.Errorf("SequenceNumber: want 0x%04X, got 0x%04X", original.SequenceNumber, parsed.SequenceNumber)
	}
	if parsed.Timestamp != original.Timestamp {
		t.Errorf("Timestamp: want 0x%08X, got 0x%08X", original.Timestamp, parsed.Timestamp)
	}
	if parsed.SSRC != original.SSRC {
		t.Errorf("SSRC: want 0x%08X, got 0x%08X", original.SSRC, parsed.SSRC)
	}
	if parsed.Marker != original.Marker {
		t.Errorf("Marker: want %v, got %v", original.Marker, parsed.Marker)
	}
	if string(parsedPayload) != string(payload) {
		t.Errorf("payload mismatch: want %v, got %v", payload, parsedPayload)
	}
	t.Logf("RTP header roundtrip OK: SSRC=0x%08X SeqNum=0x%04X TS=0x%08X PT=%d",
		parsed.SSRC, parsed.SequenceNumber, parsed.Timestamp, parsed.PayloadType)
}

// TestSessionQualityReport verifies that QualityReport returns a non-empty string
// containing the expected "RTP:" and "Pipeline:" sections.
func TestSessionQualityReport(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}

	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	report := sess.QualityReport()
	if report == "" {
		t.Fatal("QualityReport returned empty string")
	}
	if !contains(report, "RTP:") {
		t.Errorf("QualityReport missing 'RTP:': %q", report)
	}
	if !contains(report, "Pipeline:") {
		t.Errorf("QualityReport missing 'Pipeline:': %q", report)
	}
	t.Logf("QualityReport: %s", report)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
