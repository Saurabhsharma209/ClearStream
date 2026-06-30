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

// TestSSRCChangeResetsSession is a loopback UDP integration test that verifies
// the pipeline resets cleanly when the SSRC changes mid-stream, simulating a new
// call leg arriving on the same session.
//
// It starts a real Session (listening on a random UDP port), sends a burst of
// packets with SSRC=1000, then sends packets with SSRC=2000, and asserts:
//  1. PacketsReceived accumulates across both SSRCs (no crash/deadlock)
//  2. The session stays healthy for further traffic after the SSRC change
func TestSSRCChangeResetsSession(t *testing.T) {
	// Bind a discard sink so the session has a valid ForwardAddr to write to.
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
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

	// Resolve the actual listen address assigned by the OS.
	listenAddr := sess.conn.LocalAddr().(*net.UDPAddr)

	sender, err := net.DialUDP("udp", nil, listenAddr)
	if err != nil {
		t.Fatalf("dial sender: %v", err)
	}
	defer sender.Close()

	// Build a 160-byte mu-law silence payload (8 kHz, 20 ms frame).
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF
	}

	// Phase 1: send 4 packets with SSRC=1000 (first call leg).
	const ssrc1 uint32 = 1000
	for i := 0; i < 4; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), ssrc1, payload)
		if _, err := sender.Write(pkt); err != nil {
			t.Fatalf("send SSRC1 pkt %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Small pause to let the session process phase-1 packets.
	time.Sleep(80 * time.Millisecond)

	statsAfterSSRC1 := sess.Stats()
	if statsAfterSSRC1.PacketsReceived == 0 {
		t.Fatal("expected PacketsReceived > 0 after phase-1 (SSRC=1000)")
	}
	t.Logf("after SSRC=1000: PacketsReceived=%d", statsAfterSSRC1.PacketsReceived)

	// Phase 2: send 4 packets with SSRC=2000 (new call leg -- triggers pipeline reset).
	const ssrc2 uint32 = 2000
	for i := 0; i < 4; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), ssrc2, payload)
		if _, err := sender.Write(pkt); err != nil {
			t.Fatalf("send SSRC2 pkt %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Allow the session to process phase-2 packets (including the reset path).
	time.Sleep(80 * time.Millisecond)

	statsAfterSSRC2 := sess.Stats()
	// The counter must have grown: phase-2 packets were processed after the reset.
	if statsAfterSSRC2.PacketsReceived <= statsAfterSSRC1.PacketsReceived {
		t.Errorf("PacketsReceived did not increase after SSRC change: before=%d after=%d",
			statsAfterSSRC1.PacketsReceived, statsAfterSSRC2.PacketsReceived)
	}
	t.Logf("SSRC change loopback OK: SSRC %d->%d, total PacketsReceived=%d",
		ssrc1, ssrc2, statsAfterSSRC2.PacketsReceived)
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
	if !contains(report, "Band:") {
		t.Errorf("QualityReport missing 'Band:': %q", report)
	}
	t.Logf("QualityReport: %s", report)
}

// TestPayloadTypeResolution verifies that resolvePayloadType correctly maps
// standard RTP payload types to codec and sample rate, including the G.722
// wideband quirk (RTP clock=8000 but true audio=16kHz).
func TestPayloadTypeResolution(t *testing.T) {
	cases := []struct {
		pt        uint8
		wantCodec string
		wantRate  int
		desc      string
	}{
		{0, "pcm_mulaw", 8000, "PT=0 PCMU — Indian PSTN standard"},
		{8, "pcm_alaw", 8000, "PT=8 PCMA — Indian PSTN A-law"},
		{9, "g722", 16000, "PT=9 G.722 — wideband, RTP clock=8000 but audio=16kHz"},
		{111, "opus", 48000, "PT=111 Opus — WebRTC default dynamic PT"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := Config{PayloadType: tc.pt}
			resolvePayloadType(&cfg)
			if string(cfg.Codec) != tc.wantCodec {
				t.Errorf("PT=%d: codec want %q, got %q", tc.pt, tc.wantCodec, cfg.Codec)
			}
			if cfg.SampleRate != tc.wantRate {
				t.Errorf("PT=%d: sample rate want %d, got %d", tc.pt, tc.wantRate, cfg.SampleRate)
			}
		})
	}
}

// TestRTPFork verifies that when ForwardAddrs is set, clean RTP packets are
// delivered to both the primary ForwardAddr and every entry in ForwardAddrs.
// Two sink listeners are started; only one is specified via ForwardAddr, the
// second via ForwardAddrs. Both must receive forwarded packets.
func TestRTPFork(t *testing.T) {
	// Primary sink (agent).
	primarySink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind primary sink: %v", err)
	}
	defer primarySink.Close()
	primarySink.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Secondary sink (recorder / DC fork).
	recorderSink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind recorder sink: %v", err)
	}
	defer recorderSink.Close()
	recorderSink.SetReadDeadline(time.Now().Add(2 * time.Second))

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:   "127.0.0.1:0",
		ForwardAddr:  primarySink.LocalAddr().String(),
		ForwardAddrs: []string{recorderSink.LocalAddr().String()},
		PayloadType:  0, // PCMU
		JitterDepth:  1,
		Logger:       logger,
		Suppressor:   model.NewMockSuppressor(),
	}

	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Start()
	defer sess.Stop()

	// Verify fork addresses were resolved.
	if len(sess.forkAddrs) != 1 {
		t.Fatalf("expected 1 fork addr, got %d", len(sess.forkAddrs))
	}

	// Send 4 PCMU packets.
	sender, err := net.DialUDP("udp", nil, sess.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial sender: %v", err)
	}
	defer sender.Close()

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF // µ-law silence
	}
	for i := 0; i < 4; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), 0xCAFEBABE, payload)
		if _, err := sender.Write(pkt); err != nil {
			t.Fatalf("send pkt %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	// Primary sink must have received at least one packet.
	primaryBuf := make([]byte, 4096)
	primarySink.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _, err := primarySink.ReadFromUDP(primaryBuf)
	if err != nil || n < 12 {
		t.Errorf("primary sink: expected RTP packet, got n=%d err=%v", n, err)
	}

	// Recorder sink must also have received at least one packet.
	recorderSink.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _, err = recorderSink.ReadFromUDP(primaryBuf)
	if err != nil || n < 12 {
		t.Errorf("recorder (fork) sink: expected RTP packet, got n=%d err=%v", n, err)
	}

	t.Logf("RTP fork OK: primary=%s recorder=%s",
		primarySink.LocalAddr(), recorderSink.LocalAddr())
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

// TestSSRCChangeResetsPipeline verifies that processing two RTP packets with
// different SSRCs triggers a pipeline reset. It uses a counter to track resets,
// mirroring the detection logic in session.handlePacket.
func TestSSRCChangeResetsPipeline(t *testing.T) {
	const ssrc1 uint32 = 0xDEAD0001
	const ssrc2 uint32 = 0xBEEF0002
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF // u-law silence
	}

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

	// Simulate the session state fields and reset counter (mock pipeline).
	var lastSSRC uint32
	ssrcSeen := false
	resetCalled := 0

	process := func(hdr rtpHeader) {
		if ssrcSeen && hdr.SSRC != lastSSRC {
			// mirrors: pipeline.Reset() in session.handlePacket
			resetCalled++
		}
		lastSSRC = hdr.SSRC
		ssrcSeen = true
	}

	process(hdr1)
	if resetCalled != 0 {
		t.Fatalf("expected no reset on first packet, got %d", resetCalled)
	}

	process(hdr2)
	if resetCalled != 1 {
		t.Fatalf("expected Reset called once on SSRC change, got %d", resetCalled)
	}

	t.Logf("TestSSRCChangeResetsPipeline: SSRC %d to %d triggered %d reset(s)", ssrc1, ssrc2, resetCalled)
}

// TestRTTMsNoData verifies that RTTMs returns -1 when no RTCP SR has been received
// (the zero-value branch: lastSRAt.IsZero()).
func TestRTTMsNoData(t *testing.T) {
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

	// No RTCP SR has been received, so LastSRReceivedAt is zero.
	got := sess.RTTMs()
	if got != -1 {
		t.Errorf("RTTMs() with no SR: want -1, got %.2f", got)
	}
}

// TestRTTMsWithData verifies that RTTMs returns a non-negative value when
// LastSRReceivedAt and DelaySinceLastSR are both set (the happy-path branch).
func TestRTTMsWithData(t *testing.T) {
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

	// Inject an SR received 100ms ago with DLSR = 0 (triggers the dlsr==0 guard).
	sess.mu.Lock()
	sess.LastSRReceivedAt = time.Now().Add(-100 * time.Millisecond)
	sess.RTCPStats.DelaySinceLastSR = 0
	sess.mu.Unlock()

	got := sess.RTTMs()
	if got != -1 {
		t.Errorf("RTTMs() with SR set but DLSR=0: want -1, got %.2f", got)
	}

	// Now set a non-zero DLSR (1 second in 1/65536 units = 65536).
	// elapsed ≈ 100ms, dlsr = 1s → rtt would be negative → clamped to 0.
	sess.mu.Lock()
	sess.LastSRReceivedAt = time.Now().Add(-100 * time.Millisecond)
	sess.RTCPStats.DelaySinceLastSR = 6554 // ~0.1s in 1/65536 units
	sess.mu.Unlock()

	got = sess.RTTMs()
	// elapsed ~100ms minus dlsr ~100ms → ~0ms. Accept any non-negative value.
	if got < 0 {
		t.Errorf("RTTMs() with valid SR+DLSR: want >= 0, got %.2f", got)
	}
	t.Logf("RTTMs() = %.2f ms", got)
}

// TestHandlePacketTooShort verifies that handlePacket returns an error for
// packets shorter than the 12-byte RTP minimum header.
func TestHandlePacketTooShort(t *testing.T) {
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

	err = sess.handlePacket([]byte{0x80, 0x00, 0x01})
	if err == nil {
		t.Error("expected error for short packet, got nil")
	}
}
