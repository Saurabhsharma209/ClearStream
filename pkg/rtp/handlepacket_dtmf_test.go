package rtp

// TestHandlePacketDTMF_* close a real, previously 0%-covered branch in
// handlePacket: the "header.PayloadType == s.cfg.DTMFPayloadType" dispatch
// (session.go, ~line 427-434). Every existing DTMF test (dtmf_test.go)
// exercises DTMFDetector.ParseDTMFPayload directly; none of them drive an
// actual RTP packet through Session.handlePacket with the DTMF payload
// type, so the OnDTMF-callback wiring and the parse-error warn-log path
// were both completely untested at the session level.

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// buildDTMFRTPPacket builds a minimal RTP packet carrying an RFC4733
// telephone-event payload for the given digit event code.
func buildDTMFRTPPacket(seq uint16, ts uint32, ssrc uint32, eventCode byte, end bool, volume byte, duration uint16) []byte {
	buf := make([]byte, 12+4)
	buf[0] = 0x80
	buf[1] = DTMFPayloadType // no marker bit set
	binary.BigEndian.PutUint16(buf[2:4], seq)
	binary.BigEndian.PutUint32(buf[4:8], ts)
	binary.BigEndian.PutUint32(buf[8:12], ssrc)
	buf[12] = eventCode
	endBit := byte(0)
	if end {
		endBit = 0x80
	}
	buf[13] = endBit | (volume & 0x3F)
	binary.BigEndian.PutUint16(buf[14:16], duration)
	return buf
}

func newDTMFTestSession(t *testing.T, onDTMF func(DTMFDigit)) *Session {
	t.Helper()
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	t.Cleanup(func() { sinkConn.Close() })

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
		OnDTMF:      onDTMF,
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { sess.conn.Close() })
	return sess
}

// TestHandlePacketDTMF_FiresCallback verifies a well-formed DTMF RTP packet
// routed through handlePacket is recognized (PayloadType dispatch) and
// results in OnDTMF being invoked with the correctly decoded digit --
// exercising the "digit != nil && s.cfg.OnDTMF != nil" branch.
func TestHandlePacketDTMF_FiresCallback(t *testing.T) {
	var got *DTMFDigit
	sess := newDTMFTestSession(t, func(d DTMFDigit) {
		got = &d
	})

	// Event code 5 == digit "5", end packet, volume 10, duration 800 (8000Hz -> 100ms).
	pkt := buildDTMFRTPPacket(100, 8000, 0x1234, 5, true, 10, 800)
	if err := sess.handlePacket(pkt); err != nil {
		t.Fatalf("handlePacket: unexpected error: %v", err)
	}

	if got == nil {
		t.Fatal("OnDTMF was never invoked")
	}
	if got.Digit != "5" {
		t.Errorf("Digit: want %q, got %q", "5", got.Digit)
	}
	if !got.End {
		t.Error("End: want true")
	}
	if got.Volume != 10 {
		t.Errorf("Volume: want 10, got %d", got.Volume)
	}
	if got.DurationMs != 100 {
		t.Errorf("DurationMs: want 100, got %d", got.DurationMs)
	}

	// handlePacket must not have touched the jitter buffer / regular audio
	// path for a DTMF packet -- no packets should be counted as received
	// via the normal RTP audio flow's Stats bump (that only happens in
	// receiveLoop, not handlePacket itself), so just confirm no panic/side
	// effect beyond the callback by calling Stats() safely.
	_ = sess.Stats()
}

// TestHandlePacketDTMF_NoCallbackConfigured exercises the same dispatch
// path when Config.OnDTMF is nil: handlePacket must still recognize and
// consume the DTMF packet (returning nil, not falling through to the
// audio/jitter pipeline) without panicking.
func TestHandlePacketDTMF_NoCallbackConfigured(t *testing.T) {
	sess := newDTMFTestSession(t, nil)

	pkt := buildDTMFRTPPacket(1, 0, 0xABCD, 9, false, 0, 100)
	if err := sess.handlePacket(pkt); err != nil {
		t.Fatalf("handlePacket: unexpected error: %v", err)
	}
}

// TestHandlePacketDTMF_ParseError exercises the "err != nil" warn-log branch
// inside the DTMF dispatch: a payload shorter than the required 4 bytes
// makes ParseDTMFPayload return an error, which handlePacket must log and
// swallow (returning nil, not propagating the error up to the caller).
func TestHandlePacketDTMF_ParseError(t *testing.T) {
	called := false
	sess := newDTMFTestSession(t, func(DTMFDigit) { called = true })

	// Build a DTMF-payload-type RTP packet with only a 2-byte payload
	// (needs 4) -- triggers ParseDTMFPayload's "too short" error.
	buf := make([]byte, 12+2)
	buf[0] = 0x80
	buf[1] = DTMFPayloadType
	binary.BigEndian.PutUint16(buf[2:4], 1)
	binary.BigEndian.PutUint32(buf[4:8], 0)
	binary.BigEndian.PutUint32(buf[8:12], 0x1111)
	buf[12], buf[13] = 0xAA, 0xBB

	if err := sess.handlePacket(buf); err != nil {
		t.Fatalf("handlePacket: want nil (error is logged+swallowed), got %v", err)
	}
	if called {
		t.Error("OnDTMF must not fire when ParseDTMFPayload errors")
	}
}

// TestHandlePacketDTMF_UnknownEventCode exercises ParseDTMFPayload's
// "unknown event code" error path (event code >= len(dtmfEventTable)) via
// the same handlePacket dispatch, confirming it too is logged and swallowed
// rather than propagated.
func TestHandlePacketDTMF_UnknownEventCode(t *testing.T) {
	called := false
	sess := newDTMFTestSession(t, func(DTMFDigit) { called = true })

	pkt := buildDTMFRTPPacket(1, 0, 0x2222, 200, false, 0, 100) // event code 200: out of range
	if err := sess.handlePacket(pkt); err != nil {
		t.Fatalf("handlePacket: want nil, got %v", err)
	}
	if called {
		t.Error("OnDTMF must not fire for an unknown/unparseable event code")
	}
}
