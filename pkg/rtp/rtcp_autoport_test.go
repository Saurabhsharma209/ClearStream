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
