package rtp

// This file regression-tests a real bug fixed in listenRTCP (session.go)
// on 2026-07-14: listenRTCP used to derive the RTCP port by re-parsing
// s.cfg.ListenAddr's port text and adding 1. That works fine when
// ListenAddr specifies an explicit numeric port, but breaks whenever
// ListenAddr asks the OS to auto-assign a port via "host:0" -- the
// idiomatic Go pattern for "give me any free port", used throughout this
// package's own test suite (see findFreePort/rtpPort workarounds in
// codec_test.go and session_regress_test.go, all of which deliberately
// avoid ":0" for RTCP tests specifically because of this bug). With a
// ":0" ListenAddr the configured port text is always literally "0", so
// the old code tried to bind RTCP to port 1 -- a fixed, essentially
// arbitrary port totally unrelated to the RTP socket NewSession actually
// opened via net.ListenUDP. In production this meant any deployment that
// lets the OS choose the RTP port would silently never receive RTCP
// Receiver Reports (RTTMs()/QualityReport() would never see real RR data),
// and locally would almost always fail outright (port 1 requires root).
//
// The fix reads the actual bound port back off s.conn.LocalAddr() instead
// of re-parsing the config string, so RTCP correctly binds to
// (real RTP port)+1 regardless of whether the port was explicit or
// OS-assigned.

import (
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// buildMinimalRTCPRR builds a minimal, valid 32-byte RTCP Receiver Report
// packet carrying the given source SSRC, mirroring the packet built inline
// in TestRTCPListener (codec_test.go) but factored out for reuse here.
func buildMinimalRTCPRR(sourceSSRC uint32) []byte {
	pkt := make([]byte, 32)
	pkt[0] = 0x81 // V=2, P=0, RC=1
	pkt[1] = 201  // PT=RR
	binary.BigEndian.PutUint16(pkt[2:4], 7)
	binary.BigEndian.PutUint32(pkt[4:8], 0xCAFEBABE) // sender SSRC
	binary.BigEndian.PutUint32(pkt[8:12], sourceSSRC)
	pkt[12] = 0x01 // fraction lost
	pkt[13], pkt[14], pkt[15] = 0, 0, 1
	binary.BigEndian.PutUint32(pkt[16:20], 0x1234)
	binary.BigEndian.PutUint32(pkt[20:24], 64)
	binary.BigEndian.PutUint32(pkt[24:28], 0)
	binary.BigEndian.PutUint32(pkt[28:32], 0)
	return pkt
}

// TestListenRTCP_OSAssignedPort verifies that when ListenAddr uses port 0
// (OS auto-assigns the RTP port), listenRTCP still binds to the *actual*
// bound RTP port + 1, not the literal "0+1"=1 the old string-parsing code
// would have computed. This is the direct regression test for the
// 2026-07-14 bug fix.
func TestListenRTCP_OSAssignedPort(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0", // OS-assigned port -- the bug trigger.
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0,
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

	<-sess.rtcpReady

	sess.mu.Lock()
	rtcpConn := sess.rtcpConn
	sess.mu.Unlock()
	if rtcpConn == nil {
		t.Fatal("rtcpConn is nil -- listenRTCP failed to bind at all")
	}

	rtpPort := sess.conn.LocalAddr().(*net.UDPAddr).Port
	boundRTCPPort := rtcpConn.LocalAddr().(*net.UDPAddr).Port

	if boundRTCPPort != rtpPort+1 {
		t.Fatalf("RTCP bound to port %d, want rtpPort+1=%d (actual RTP port %d)",
			boundRTCPPort, rtpPort+1, rtpPort)
	}
	if boundRTCPPort == 1 {
		t.Fatal("RTCP bound to port 1 -- the exact bug this test guards against (stale port-0 string parsing)")
	}

	// End-to-end: an RTCP RR sent to the dynamically-computed port must
	// actually reach the session and update RTCPStats.
	const srcSSRC = 0xC0FFEE01
	sender, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: boundRTCPPort})
	if err != nil {
		t.Fatalf("dial RTCP port %d: %v", boundRTCPPort, err)
	}
	defer sender.Close()
	if _, err := sender.Write(buildMinimalRTCPRR(srcSSRC)); err != nil {
		t.Fatalf("send RTCP RR: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		ssrc := sess.RTCPStats.SSRC
		sess.mu.Unlock()
		if ssrc == srcSSRC {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("RTCPStats.SSRC was never updated -- RTCP RR sent to the OS-assigned RTCP port was not received")
}

// TestListenRTCP_ExplicitPortUnchanged is a control test verifying the fix
// preserves existing behavior for the (previously only working) explicit
// numeric-port case: RTCP must still bind to exactly rtpPort+1.
func TestListenRTCP_ExplicitPortUnchanged(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	rtpPort := findFreePort(t)
	rtcpCheck, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rtpPort + 1})
	if err != nil {
		t.Skipf("port %d unavailable for RTCP: %v", rtpPort+1, err)
	}
	rtcpCheck.Close()

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:" + strconv.Itoa(rtpPort),
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0,
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

	<-sess.rtcpReady

	sess.mu.Lock()
	rtcpConn := sess.rtcpConn
	sess.mu.Unlock()
	if rtcpConn == nil {
		t.Fatal("rtcpConn is nil")
	}
	got := rtcpConn.LocalAddr().(*net.UDPAddr).Port
	if got != rtpPort+1 {
		t.Fatalf("RTCP bound to port %d, want %d", got, rtpPort+1)
	}
}

// buildMultiBlockRTCPRR builds a compound RTCP RR packet carrying two
// reception report blocks (RC=2): one for otherSSRC and one for wantSSRC,
// each with a distinguishable jitter value so a test can tell which block
// ended up in Session.RTCPStats.
func buildMultiBlockRTCPRR(otherSSRC, otherJitter, wantSSRC, wantJitter uint32) []byte {
	pkt := make([]byte, 8+2*24)
	pkt[0] = 0x82 // V=2, P=0, RC=2
	pkt[1] = 201  // PT=RR
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)/4-1))
	binary.BigEndian.PutUint32(pkt[4:8], 0xCAFEBABE) // sender SSRC

	// Block 0: otherSSRC first on the wire -- this is the block the old
	// code would have picked unconditionally.
	binary.BigEndian.PutUint32(pkt[8:12], otherSSRC)
	binary.BigEndian.PutUint32(pkt[20:24], otherJitter)

	// Block 1: the SSRC this session is actually tracking.
	binary.BigEndian.PutUint32(pkt[32:36], wantSSRC)
	binary.BigEndian.PutUint32(pkt[44:48], wantJitter)

	return pkt
}

// TestListenRTCP_MultiBlockPicksTrackedSSRC is the regression test for the
// RTCP compound-packet gap fixed alongside ParseRTCPReceiverReportBlocks: a
// peer (e.g. a conference bridge/mixer) can report on more than one SSRC in
// a single RR packet. Before this fix, Session.listenRTCP always kept
// whichever report block happened to be first on the wire, even when it
// described a completely unrelated source -- silently corrupting
// RTTMs()/QualityReport() with the wrong source's numbers. This verifies
// listenRTCP now selects the block matching the RTP SSRC the session is
// actually receiving, regardless of its position in the packet.
func TestListenRTCP_MultiBlockPicksTrackedSSRC(t *testing.T) {
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

	<-sess.rtcpReady

	const trackedSSRC = 0xDEADBEEF
	const otherSSRC = 0x11111111
	const trackedJitter = 999
	const otherJitter = 111

	// Feed one RTP packet so the session learns trackedSSRC as the stream
	// it's tracking (mirrors real traffic: RTCP always follows some RTP).
	rtpSender, err := net.DialUDP("udp", nil, sess.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial RTP: %v", err)
	}
	defer rtpSender.Close()
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF
	}
	if _, err := rtpSender.Write(buildRawRTPPacket(0, 0, trackedSSRC, payload)); err != nil {
		t.Fatalf("send RTP: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	sess.mu.Lock()
	rtcpConn := sess.rtcpConn
	sess.mu.Unlock()
	if rtcpConn == nil {
		t.Fatal("rtcpConn is nil")
	}
	boundRTCPPort := rtcpConn.LocalAddr().(*net.UDPAddr).Port

	rtcpSender, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: boundRTCPPort})
	if err != nil {
		t.Fatalf("dial RTCP port %d: %v", boundRTCPPort, err)
	}
	defer rtcpSender.Close()

	// otherSSRC's block comes first on the wire; trackedSSRC's block comes
	// second. The old code would have kept otherSSRC's numbers.
	pkt := buildMultiBlockRTCPRR(otherSSRC, otherJitter, trackedSSRC, trackedJitter)
	if _, err := rtcpSender.Write(pkt); err != nil {
		t.Fatalf("send RTCP RR: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		stats := sess.RTCPStats
		sess.mu.Unlock()
		if stats.SSRC == trackedSSRC {
			if stats.Jitter != trackedJitter {
				t.Fatalf("RTCPStats.Jitter = %d, want %d (tracked SSRC's block)", stats.Jitter, trackedJitter)
			}
			return // success
		}
		if stats.SSRC == otherSSRC {
			t.Fatalf("RTCPStats picked the untracked SSRC's block (jitter=%d) instead of the tracked SSRC's block -- compound RR block selection is broken", stats.Jitter)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("RTCPStats.SSRC never became the tracked SSRC")
}
