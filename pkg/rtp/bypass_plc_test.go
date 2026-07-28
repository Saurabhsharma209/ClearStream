package rtp

import (
	"math"
	"net"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// TestBypassModePrimesPLCFromRealAudio is a regression test for a real bug:
// the bypass fast path in handlePacket (suppressor=passthrough) forwarded
// the raw payload directly and never called jitter.onGoodPacket, so
// JitterBuffer.lastGoodFrame was never populated while bypass mode was
// active. Any subsequent packet loss then produced permanent flat silence
// from GeneratePLC no-history branch instead of the documented pitch-period
// waveform substitution PLC, even though the same bypass-mode session had
// real, decodable audio flowing through it moments earlier.
//
// This drives a real bypass-mode Session with a loud, non-silent G.711
// signal, induces a single packet loss (skip one sequence number), and
// confirms the forwarded PLC frame decodes back to non-zero-energy PCM --
// proving lastGoodFrame was actually primed from the bypass path.
func TestBypassModePrimesPLCFromRealAudio(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sink.Close()

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0,
		JitterDepth: 1,
		Logger:      logger,
		Suppressor:  model.NewPassthrough(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()
	if !sess.isBypassMode() {
		t.Fatal("expected bypass mode with Passthrough suppressor")
	}

	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(12000 * math.Sin(2*math.Pi*float64(i)/20.0))
	}
	loudPayload := encodeG711U(samples)

	pkt0 := buildRawRTPPacket(0, 0, 0xFEEDFACE, loudPayload)
	if err := sess.handlePacket(pkt0); err != nil {
		t.Fatalf("handlePacket(seq=0): %v", err)
	}

	// seq=1 is intentionally never sent -- simulates one dropped packet.

	silentPayload := make([]byte, 160)
	for i := range silentPayload {
		silentPayload[i] = 0xFF
	}
	pkt2 := buildRawRTPPacket(2, 320, 0xFEEDFACE, silentPayload)
	if err := sess.handlePacket(pkt2); err != nil {
		t.Fatalf("handlePacket(seq=2): %v", err)
	}

	sink.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	recvBuf := make([]byte, 4096)

	n, _, err := sink.ReadFromUDP(recvBuf)
	if err != nil || n == 0 {
		t.Fatalf("read forwarded packet 1: n=%d err=%v", n, err)
	}

	n, _, err = sink.ReadFromUDP(recvBuf)
	if err != nil {
		t.Fatalf("read forwarded PLC packet: %v", err)
	}
	_, plcPayload, err := parseRTPHeader(recvBuf[:n])
	if err != nil {
		t.Fatalf("parseRTPHeader on PLC packet: %v", err)
	}

	plcPCM := decodeG711U(plcPayload)
	defer putG711PCM(plcPCM)

	var energy int64
	for _, s := range plcPCM {
		energy += int64(s) * int64(s)
	}
	if energy == 0 {
		t.Fatal("PLC frame after packet loss in bypass mode is pure silence -- lastGoodFrame was never primed (bypass-mode PLC priming regression)")
	}
	t.Logf("PLC frame energy after loss in bypass mode: %d (non-zero, lastGoodFrame primed correctly)", energy)
}
