package rtp

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

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
