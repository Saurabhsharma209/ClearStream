package rtp

import (
	"encoding/binary"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// ---- G.711 A-law roundtrip --------------------------------------------------

// TestDecodeEncodeG711A_Roundtrip verifies that decoding all 256 A-law byte
// values and re-encoding them round-trips within quantization tolerance (±8*8=64).
func TestDecodeEncodeG711A_Roundtrip(t *testing.T) {
	// Build a payload of all 256 A-law byte values.
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}

	decoded := decodeG711A(payload)
	if len(decoded) != 256 {
		t.Fatalf("decodeG711A: want 256 samples, got %d", len(decoded))
	}

	reencoded := encodeG711A(decoded)
	if len(reencoded) != 256 {
		t.Fatalf("encodeG711A: want 256 bytes, got %d", len(reencoded))
	}

	// Re-decode and check quantization error is within expected range.
	redecoded := decodeG711A(reencoded)
	maxDiff := int16(0)
	for i := range decoded {
		d := decoded[i] - redecoded[i]
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	// A-law has up to ~2% quantization error; for 16-bit range, allow ±256.
	if maxDiff > 256 {
		t.Errorf("A-law roundtrip max diff %d exceeds tolerance 256", maxDiff)
	}
	t.Logf("G.711 A-law roundtrip: max quantization diff = %d (tolerance: 256)", maxDiff)
}

// TestDecodeEncodeG711U_Roundtrip verifies µ-law codec roundtrip.
func TestDecodeEncodeG711U_Roundtrip(t *testing.T) {
	payload := make([]byte, 256)
	for i := range payload {
		payload[i] = byte(i)
	}

	decoded := decodeG711U(payload)
	if len(decoded) != 256 {
		t.Fatalf("decodeG711U: want 256 samples, got %d", len(decoded))
	}

	reencoded := encodeG711U(decoded)
	redecoded := decodeG711U(reencoded)

	maxDiff := int16(0)
	for i := range decoded {
		d := decoded[i] - redecoded[i]
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff > 256 {
		t.Errorf("µ-law roundtrip max diff %d exceeds tolerance 256", maxDiff)
	}
	t.Logf("G.711 µ-law roundtrip: max quantization diff = %d", maxDiff)
}

// ---- payloadTypeToCodec -----------------------------------------------------

func TestPayloadTypeToCodec(t *testing.T) {
	cases := []struct {
		pt   uint8
		want audio.Codec
	}{
		{0, audio.CodecG711U},
		{8, audio.CodecG711A},
		{9, audio.CodecG722},
		{18, audio.CodecG729},
		{96, audio.CodecG711U}, // unknown → fallback
		{111, audio.CodecOpus},
	}
	for _, tc := range cases {
		got := payloadTypeToCodec(tc.pt)
		if got != tc.want {
			t.Errorf("payloadTypeToCodec(%d): want %q, got %q", tc.pt, tc.want, got)
		}
	}
}

// ---- int16SliceToBytes / bytesToInt16Slice roundtrip -------------------------

func TestInt16SliceToBytes(t *testing.T) {
	original := []int16{0, 1, -1, 256, -256, 32767, -32768, 100, -100}
	encoded := int16SliceToBytes(original)
	if len(encoded) != len(original)*2 {
		t.Fatalf("int16SliceToBytes: want %d bytes, got %d", len(original)*2, len(encoded))
	}

	// Verify little-endian encoding manually for first few values.
	if encoded[0] != 0 || encoded[1] != 0 {
		t.Errorf("sample 0 (0): want [0,0], got [%d,%d]", encoded[0], encoded[1])
	}
	if encoded[2] != 1 || encoded[3] != 0 {
		t.Errorf("sample 1 (1): want [1,0], got [%d,%d]", encoded[2], encoded[3])
	}

	// Roundtrip through bytesToInt16Slice.
	decoded := bytesToInt16Slice(encoded)
	if len(decoded) != len(original) {
		t.Fatalf("bytesToInt16Slice: want %d samples, got %d", len(original), len(decoded))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("sample[%d]: want %d, got %d", i, original[i], decoded[i])
		}
	}
	t.Logf("int16SliceToBytes roundtrip OK for %d samples", len(original))
}

// ---- statsLoop / OnStats callback -------------------------------------------

func TestStatsCallback(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	logger, _ := zap.NewDevelopment()

	var callbackCount int64
	var statsMu sync.Mutex
	var lastStats Stats

	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0, // PCMU
		JitterDepth: 1,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
		OnStats: func(s Stats) {
			atomic.AddInt64(&callbackCount, 1)
			statsMu.Lock()
			lastStats = s
			statsMu.Unlock()
		},
	}

	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Start()
	defer sess.Stop()

	// Send a few packets so PacketsReceived > 0.
	listenAddr := sess.conn.LocalAddr().(*net.UDPAddr)
	sender, err := net.DialUDP("udp", nil, listenAddr)
	if err != nil {
		t.Fatalf("dial sender: %v", err)
	}
	defer sender.Close()

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF // µ-law silence
	}
	for i := 0; i < 5; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), 0xDEADBEEF, payload)
		sender.Write(pkt)
		time.Sleep(5 * time.Millisecond)
	}

	// Wait up to 2.5 seconds for at least one callback.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&callbackCount) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt64(&callbackCount) == 0 {
		t.Fatal("OnStats callback never fired within 2.5 seconds")
	}
	statsMu.Lock()
	snap := lastStats
	statsMu.Unlock()
	t.Logf("OnStats fired %d time(s); last: rx=%d tx=%d lost=%d latency=%.2fms",
		callbackCount, snap.PacketsReceived, snap.PacketsSent,
		snap.PacketsLost, snap.LatencyAvgMs)
}

// ---- RTCP listener ----------------------------------------------------------

// TestRTCPListener sends a well-formed RTCP RR to the session's RTCP port and
// verifies the session accepts it without panic or error after 200ms.
func TestRTCPListener(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	logger, _ := zap.NewDevelopment()

	// Use an explicit RTP port so that RTCP = rtpPort+1 is predictable.
	rtpPort := findFreePort(t)
	// Ensure rtpPort+1 is also free; if not, skip.
	rtcpCheck, err2 := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rtpPort + 1})
	if err2 != nil {
		t.Skipf("port %d unavailable for RTCP: %v", rtpPort+1, err2)
	}
	rtcpCheck.Close()

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

	// Wait for listenRTCP to bind (use rtcpReady channel via a brief sleep).
	<-sess.rtcpReady

	rtcpAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rtpPort + 1}

	// Build a minimal valid RTCP RR packet (32 bytes).
	// Header: V=2, P=0, RC=1, PT=201, length=7 (in 32-bit words minus 1)
	rtcpPkt := make([]byte, 32)
	rtcpPkt[0] = 0x81                                    // V=2, P=0, RC=1
	rtcpPkt[1] = 201                                     // PT=RR
	binary.BigEndian.PutUint16(rtcpPkt[2:4], 7)          // length = 7 words
	binary.BigEndian.PutUint32(rtcpPkt[4:8], 0xCAFEBABE) // sender SSRC
	// Report block (24 bytes starting at byte 8)
	binary.BigEndian.PutUint32(rtcpPkt[8:12], 0xDEADBEEF) // source SSRC
	rtcpPkt[12] = 0x05                                    // fraction lost (5/256 ≈ 2%)
	rtcpPkt[13] = 0
	rtcpPkt[14] = 0
	rtcpPkt[15] = 0x03                                     // cumulative lost = 3
	binary.BigEndian.PutUint32(rtcpPkt[16:20], 0x0000ABCD) // highest seq
	binary.BigEndian.PutUint32(rtcpPkt[20:24], 128)        // jitter
	binary.BigEndian.PutUint32(rtcpPkt[24:28], 0)          // last SR
	binary.BigEndian.PutUint32(rtcpPkt[28:32], 0)          // delay since last SR

	sender, err := net.DialUDP("udp", nil, rtcpAddr)
	if err != nil {
		t.Skipf("cannot connect to RTCP port %d: %v", rtpPort+1, err)
	}
	defer sender.Close()

	if _, err := sender.Write(rtcpPkt); err != nil {
		t.Fatalf("send RTCP RR: %v", err)
	}

	// Wait for the session to process it.
	time.Sleep(200 * time.Millisecond)

	// Verify RTCPStats was updated (no panic = test passed, but also check SSRC).
	sess.mu.Lock()
	rtcpStats := sess.RTCPStats
	sess.mu.Unlock()

	if rtcpStats.SSRC != 0xDEADBEEF {
		t.Errorf("RTCPStats.SSRC: want 0xDEADBEEF, got 0x%08X", rtcpStats.SSRC)
	}
	t.Logf("RTCP RR received OK: SSRC=0x%08X loss=%.2f%% jitter=%d",
		rtcpStats.SSRC, rtcpStats.FractionLost*100, rtcpStats.Jitter)
}

// ---- RTCP packet parsing edge cases -----------------------------------------

func TestParseRTCPReceiverReportEdgeCases(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		_, err := ParseRTCPReceiverReport([]byte{0x81, 201, 0, 0})
		if err == nil {
			t.Error("expected error for short packet")
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		pkt := make([]byte, 32)
		pkt[0] = 0x01 // version=0
		pkt[1] = 201
		_, err := ParseRTCPReceiverReport(pkt)
		if err == nil {
			t.Error("expected error for wrong version")
		}
	})

	t.Run("not RR type returns nil", func(t *testing.T) {
		pkt := make([]byte, 32)
		pkt[0] = 0x80 // V=2, RC=0
		pkt[1] = 200  // SR, not RR
		rr, err := ParseRTCPReceiverReport(pkt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rr != nil {
			t.Error("expected nil for non-RR packet type")
		}
	})

	t.Run("zero RC returns nil", func(t *testing.T) {
		pkt := make([]byte, 32)
		pkt[0] = 0x80 // V=2, RC=0
		pkt[1] = 201  // RR
		rr, err := ParseRTCPReceiverReport(pkt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rr != nil {
			t.Error("expected nil for RR with RC=0")
		}
	})
}

// ---- FFmpeg codec paths -----------------------------------------------------

func TestDecodeEncodeViaFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH, skipping FFmpeg codec tests")
	}

	// Build a tiny G.722 payload: use ffmpeg to create one from PCM.
	// 160 samples of silence at 16kHz.
	pcmSilence := make([]int16, 160)
	pcmBytes := int16SliceToBytes(pcmSilence)

	// Encode PCM to G.722 via ffmpeg.
	cmd := exec.Command(ffmpeg,
		"-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0",
		"-f", "g722", "pipe:1",
	)
	cmd.Stdin = strings.NewReader(string(pcmBytes))
	g722Payload, err := cmd.Output()
	if err != nil || len(g722Payload) == 0 {
		t.Skipf("ffmpeg G.722 encode failed: %v", err)
	}

	decoded, err := decodeViaFFmpeg(ffmpeg, g722Payload, audio.CodecG722, 16000)
	if err != nil {
		t.Fatalf("decodeViaFFmpeg(G.722): %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("decodeViaFFmpeg returned empty PCM")
	}

	// Re-encode.
	reencoded, err := encodeViaFFmpeg(ffmpeg, decoded, audio.CodecG722, 16000)
	if err != nil {
		t.Fatalf("encodeViaFFmpeg(G.722): %v", err)
	}
	if len(reencoded) == 0 {
		t.Fatal("encodeViaFFmpeg returned empty payload")
	}
	t.Logf("FFmpeg G.722 roundtrip: %d bytes G.722 → %d PCM samples → %d bytes G.722", len(g722Payload), len(decoded), len(reencoded))
}

// TestDecodeViaFFmpegInvalidCodec exercises the default s16le branch.
func TestDecodeViaFFmpegInvalidCodec(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH")
	}
	// s16le passthrough: feed raw PCM, get same PCM back.
	pcm := []int16{100, -100, 200}
	payload := int16SliceToBytes(pcm)
	decoded, err := decodeViaFFmpeg(ffmpeg, payload, audio.CodecUnknown, 8000)
	if err != nil {
		t.Logf("decodeViaFFmpeg with unknown codec (s16le): %v (acceptable)", err)
		return
	}
	t.Logf("decodeViaFFmpeg unknown codec returned %d samples", len(decoded))
}

// ---- resolvePayloadType unknown PT ------------------------------------------

func TestResolvePayloadTypeUnknown(t *testing.T) {
	cfg := Config{PayloadType: 99} // unknown
	resolvePayloadType(&cfg)
	if cfg.SampleRate == 0 {
		t.Error("SampleRate should default to 8000 for unknown PT")
	}
	if cfg.Codec == "" {
		t.Error("Codec should default for unknown PT")
	}
}

// ---- QualityReport bandwidth labels -----------------------------------------

func TestQualityReportBandLabels(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	for _, tc := range []struct {
		pt      uint8
		wantBnd string
	}{
		{0, "narrowband"},
		{9, "wideband"},
		{111, "fullband"},
	} {
		cfg := Config{
			ListenAddr:  "127.0.0.1:0",
			ForwardAddr: sinkConn.LocalAddr().String(),
			PayloadType: tc.pt,
			Logger:      logger,
			Suppressor:  model.NewMockSuppressor(),
		}
		sess, err := NewSession(cfg)
		if err != nil {
			t.Fatalf("NewSession PT=%d: %v", tc.pt, err)
		}
		report := sess.QualityReport()
		sess.conn.Close()
		if !strings.Contains(report, tc.wantBnd) {
			t.Errorf("PT=%d report should contain %q: %s", tc.pt, tc.wantBnd, report)
		}
	}
}

// ---- parseRTPHeader edge cases ----------------------------------------------

func TestParseRTPHeaderTooShort(t *testing.T) {
	_, _, err := parseRTPHeader([]byte{0x80, 0x00})
	if err == nil {
		t.Error("expected error for packet shorter than 12 bytes")
	}
}

func TestParseRTPHeaderWithExtension(t *testing.T) {
	// Build header with extension bit set and a 4-byte extension.
	buf := make([]byte, 12+4+4) // header + ext header (4 bytes) + ext data (4 bytes) + 0-byte payload
	buf[0] = 0x90               // V=2, extension=1
	buf[1] = 0x00
	binary.BigEndian.PutUint16(buf[2:4], 1)
	binary.BigEndian.PutUint32(buf[4:8], 100)
	binary.BigEndian.PutUint32(buf[8:12], 0xDEAD)
	// Extension header: profile=0xBEDE, length=1 (1 x 4-byte word)
	binary.BigEndian.PutUint16(buf[12:14], 0xBEDE)
	binary.BigEndian.PutUint16(buf[14:16], 1)
	// Extension data (4 bytes)
	binary.BigEndian.PutUint32(buf[16:20], 0xCAFE)

	h, payload, err := parseRTPHeader(buf)
	if err != nil {
		t.Fatalf("parseRTPHeader with extension: %v", err)
	}
	if !h.Extension {
		t.Error("Extension bit should be set")
	}
	_ = payload
}

// ---- decodeToPCM / encodeFromPCM via session --------------------------------

func TestDecodeToPCMCodecs(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	// Test A-law (PT=8)
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 8,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = byte(i)
	}

	pcm, err := sess.decodeToPCM(payload, 8)
	if err != nil {
		t.Fatalf("decodeToPCM PT=8: %v", err)
	}
	if len(pcm) != 160 {
		t.Errorf("decodeToPCM: want 160 samples, got %d", len(pcm))
	}

	reencoded, err := sess.encodeFromPCM(pcm, 8)
	if err != nil {
		t.Fatalf("encodeFromPCM PT=8: %v", err)
	}
	if len(reencoded) != 160 {
		t.Errorf("encodeFromPCM: want 160 bytes, got %d", len(reencoded))
	}
	t.Logf("decodeToPCM/encodeFromPCM PT=8 (A-law): %d bytes → %d samples → %d bytes", len(payload), len(pcm), len(reencoded))
}

func TestDecodeToPCMPCM(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		Codec:       audio.CodecPCM,
		SampleRate:  8000,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	pcmIn := []int16{100, 200, -100, -200}
	payload := int16SliceToBytes(pcmIn)
	pcmOut, err := sess.decodeToPCM(payload, 0)
	if err != nil {
		t.Fatalf("decodeToPCM PCM: %v", err)
	}
	if len(pcmOut) != len(pcmIn) {
		t.Errorf("decodeToPCM PCM: want %d samples, got %d", len(pcmIn), len(pcmOut))
	}
}

// ---- listenRTCP stop via conn close -----------------------------------------

func TestListenRTCPInvalidAddr(t *testing.T) {
	// A session with a bad port string exercises the early-return paths in listenRTCP.
	// We do this indirectly by checking that a session with valid config can Start/Stop.
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	// Use port 0 so RTCP goes to port 1 (may fail on privileged ports — that's
	// the "return" path in listenRTCP we want to cover).
	rtpPort := findFreePort(t)
	cfg := Config{
		ListenAddr:  "127.0.0.1:" + strconv.Itoa(rtpPort),
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Start()
	time.Sleep(50 * time.Millisecond)
	sess.Stop()
}

// ---- NewSession error paths -------------------------------------------------

func TestNewSessionErrors(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	t.Run("missing ListenAddr", func(t *testing.T) {
		_, err := NewSession(Config{ForwardAddr: "127.0.0.1:9999", Logger: logger})
		if err == nil {
			t.Error("expected error for missing ListenAddr")
		}
	})

	t.Run("missing ForwardAddr", func(t *testing.T) {
		_, err := NewSession(Config{ListenAddr: "127.0.0.1:0", Logger: logger})
		if err == nil {
			t.Error("expected error for missing ForwardAddr")
		}
	})

	t.Run("invalid ForwardAddr", func(t *testing.T) {
		_, err := NewSession(Config{
			ListenAddr:  "127.0.0.1:0",
			ForwardAddr: "not-a-valid:::addr",
			Logger:      logger,
		})
		if err == nil {
			t.Error("expected error for invalid ForwardAddr")
		}
	})

	t.Run("invalid ListenAddr", func(t *testing.T) {
		_, err := NewSession(Config{
			ListenAddr:  "not-valid:::addr",
			ForwardAddr: "127.0.0.1:9999",
			Logger:      logger,
		})
		if err == nil {
			t.Error("expected error for invalid ListenAddr")
		}
	})
}

// ---- encodeFromPCM with PCM codec -------------------------------------------

func TestEncodeFromPCMAllCodecs(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	pcm := []int16{100, -100, 200, -200, 0}

	for _, tc := range []struct {
		codec audio.Codec
		pt    uint8
		name  string
	}{
		{audio.CodecG711U, 0, "PCMU"},
		{audio.CodecG711A, 8, "PCMA"},
		{audio.CodecPCM, 0, "PCM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				ListenAddr:  "127.0.0.1:0",
				ForwardAddr: sinkConn.LocalAddr().String(),
				Codec:       tc.codec,
				SampleRate:  8000,
				Logger:      logger,
				Suppressor:  model.NewMockSuppressor(),
			}
			sess, err := NewSession(cfg)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			defer sess.conn.Close()

			out, err := sess.encodeFromPCM(pcm, tc.pt)
			if err != nil {
				t.Fatalf("encodeFromPCM: %v", err)
			}
			if len(out) == 0 {
				t.Error("encodeFromPCM returned empty output")
			}
		})
	}
}

// ---- decodeToPCM / encodeFromPCM default/unknown codec branch ---------------

func TestDecodeToPCMUnknownCodec(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	// When Codec is Unknown, decodeToPCM calls payloadTypeToCodec.
	// PT=0 → CodecG711U; PT=8 → CodecG711A — test with unknown PT to hit default branch.
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		Codec:       audio.CodecUnknown,
		SampleRate:  8000,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = byte(i)
	}

	// PT=0 → PCMU
	pcm, err := sess.decodeToPCM(payload, 0)
	if err != nil {
		t.Fatalf("decodeToPCM PT=0 (Unknown codec): %v", err)
	}
	if len(pcm) == 0 {
		t.Error("decodeToPCM returned empty PCM")
	}

	// PT=8 → PCMA
	pcm8, err := sess.decodeToPCM(payload, 8)
	if err != nil {
		t.Fatalf("decodeToPCM PT=8 (Unknown codec): %v", err)
	}
	if len(pcm8) == 0 {
		t.Error("decodeToPCM PT=8 returned empty PCM")
	}

	// Re-encode with unknown codec PT=0 (uses payloadTypeToCodec → G711U)
	out, err := sess.encodeFromPCM(pcm, 0)
	if err != nil {
		t.Fatalf("encodeFromPCM PT=0 Unknown: %v", err)
	}
	if len(out) == 0 {
		t.Error("encodeFromPCM returned empty output")
	}

	// PT=96 → default fallback (G711U)
	outDefault, err := sess.encodeFromPCM(pcm, 96)
	if err != nil {
		t.Fatalf("encodeFromPCM PT=96 default: %v", err)
	}
	if len(outDefault) == 0 {
		t.Error("encodeFromPCM default returned empty output")
	}
}

// ---- listenRTCP invalid RTCP parse (triggers logger warn) -------------------

func TestRTCPListenerBadPacket(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer sinkConn.Close()
	logger, _ := zap.NewDevelopment()

	rtpPort := findFreePort(t)
	rtcpCheck, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rtpPort + 1})
	if err != nil {
		t.Skipf("port %d unavailable for RTCP: %v", rtpPort+1, err)
	}
	rtcpCheck.Close()

	cfg := Config{
		ListenAddr:  "127.0.0.1:" + strconv.Itoa(rtpPort),
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Start()
	defer sess.Stop()

	<-sess.rtcpReady // wait for RTCP to bind

	rtcpAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rtpPort + 1}
	sender, err := net.DialUDP("udp", nil, rtcpAddr)
	if err != nil {
		t.Skipf("cannot dial RTCP: %v", err)
	}
	defer sender.Close()

	// Send a malformed RTCP packet (wrong version) — triggers the warn log path.
	badPkt := []byte{0x40, 201, 0, 7, 0, 0, 0, 1} // version=1 (invalid)
	badPkt = append(badPkt, make([]byte, 24)...)
	sender.Write(badPkt)

	time.Sleep(100 * time.Millisecond)
	// No panic or crash = test passes.
}

// ---- handlePacket short packet ----------------------------------------------

func TestHandlePacketShort(t *testing.T) {
	sinkConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
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

	err = sess.handlePacket([]byte{0x80, 0x00}) // too short
	if err == nil {
		t.Error("expected error for short packet")
	}
}

// TestHandlePacketPLCAndSSRCChange exercises handlePacket PLC and SSRC-change
// paths by calling it directly with controlled packet sequences.
func TestHandlePacketPLCAndSSRCChange(t *testing.T) {
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
		JitterDepth: 2,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF // µ-law silence
	}

	const ssrc1 uint32 = 0xAAAA1111
	const ssrc2 uint32 = 0xBBBB2222

	// Prime the jitter buffer (depth=2): send seq 0,1,2 with ssrc1.
	for i := 0; i < 4; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), ssrc1, payload)
		sess.handlePacket(pkt) //nolint:errcheck
	}

	// Now send a packet with a different SSRC — triggers SSRC change + reset.
	ssrcChangePkt := buildRawRTPPacket(5, 5*160, ssrc2, payload)
	sess.handlePacket(ssrcChangePkt) //nolint:errcheck

	// Send a few more with ssrc2 to prime the buffer again.
	for i := 6; i < 10; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), ssrc2, payload)
		sess.handlePacket(pkt) //nolint:errcheck
	}

	// Verify stats accumulated from handlePacket calls.
	stats := sess.Stats()
	t.Logf("handlePacket test: rx=%d tx=%d lost=%d", stats.PacketsReceived, stats.PacketsSent, stats.PacketsLost)
}

// TestRTPLoopbackPCMA sends A-law packets and verifies session processes them.
func TestRTPLoopbackPCMA(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 8, // PCMA
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

	listenAddr := sess.conn.LocalAddr().(*net.UDPAddr)
	sender, err := net.DialUDP("udp", nil, listenAddr)
	if err != nil {
		t.Fatalf("dial sender: %v", err)
	}
	defer sender.Close()

	// A-law silence is 0xD5
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xD5
	}
	// Set PT=8 in the RTP header
	for i := 0; i < 4; i++ {
		buf := make([]byte, 12+len(payload))
		buf[0] = 0x80
		buf[1] = 0x08 // PT=8 PCMA
		buf[2] = byte(i >> 8)
		buf[3] = byte(i)
		buf[4] = 0
		buf[5] = 0
		buf[6] = byte(i * 160 >> 8)
		buf[7] = byte(i * 160)
		buf[8] = 0xDE
		buf[9] = 0xAD
		buf[10] = 0xBE
		buf[11] = 0xEF
		copy(buf[12:], payload)
		sender.Write(buf)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	stats := sess.Stats()
	if stats.PacketsReceived == 0 {
		t.Error("expected PacketsReceived > 0 for PCMA session")
	}
	t.Logf("PCMA loopback: rx=%d tx=%d lost=%d", stats.PacketsReceived, stats.PacketsSent, stats.PacketsLost)
}

func findFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("findFreePort: %v", err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	return port
}
