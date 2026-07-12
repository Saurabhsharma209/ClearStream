package rtp

import (
	"net"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// newCleanAudioTestSession builds a loopback RTP session identical in shape to
// TestRTPLoopback (see session_test.go), parameterised on CleanAudioBufferSize
// so each test below can exercise the clean-audio feed decision:
// Session.CleanAudio() delivers owned copies of post-suppression PCM via a
// bounded, non-blocking, drop-oldest-on-full channel (Config.CleanAudioBufferSize).
func newCleanAudioTestSession(t *testing.T, bufSize int) (*Session, *net.UDPConn) {
	t.Helper()

	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	t.Cleanup(func() { sinkConn.Close() })
	sinkAddr := sinkConn.LocalAddr().(*net.UDPAddr)

	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:           "127.0.0.1:0",
		ForwardAddr:          sinkAddr.String(),
		PayloadType:          0, // PCMU
		JitterDepth:          1,
		Logger:               logger,
		Suppressor:           model.NewMockSuppressor(),
		CleanAudioBufferSize: bufSize,
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.Start()
	t.Cleanup(sess.Stop)

	listenAddr := sess.conn.LocalAddr().(*net.UDPAddr)
	sender, err := net.DialUDP("udp", nil, listenAddr)
	if err != nil {
		t.Fatalf("dial sender: %v", err)
	}
	t.Cleanup(func() { sender.Close() })
	return sess, sender
}

func sendSilentPCMUPackets(t *testing.T, sender *net.UDPConn, n int) {
	t.Helper()
	payload := make([]byte, 160) // 20ms @ 8kHz PCMU
	for i := range payload {
		payload[i] = 0xFF // mu-law silence
	}
	for i := 0; i < n; i++ {
		pkt := buildRawRTPPacket(uint16(i), uint32(i*160), 0xC0FFEE, payload)
		if _, err := sender.Write(pkt); err != nil {
			t.Fatalf("send packet %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCleanAudio_DisabledByDefault verifies the zero-cost default: when
// Config.CleanAudioBufferSize is unset (0), CleanAudio() returns a nil
// channel and existing telephony-only sessions are entirely unaffected.
func TestCleanAudio_DisabledByDefault(t *testing.T) {
	sess, sender := newCleanAudioTestSession(t, 0)

	if ch := sess.CleanAudio(); ch != nil {
		t.Fatalf("expected nil channel when CleanAudioBufferSize is 0, got non-nil")
	}

	// Sanity: the session still processes packets normally with the feed disabled.
	sendSilentPCMUPackets(t, sender, 4)
	time.Sleep(150 * time.Millisecond)
	if stats := sess.Stats(); stats.PacketsReceived == 0 {
		t.Errorf("expected PacketsReceived > 0 with clean-audio feed disabled, got 0")
	}
}

// TestCleanAudio_DeliversOwnedFrames verifies the core decision: enabling
// CleanAudioBufferSize delivers real, non-empty PCM frames, and that each
// delivered frame is an owned copy -- not aliased to the pooled cleanPCM
// buffer that handlePacket reuses on the very next packet.
func TestCleanAudio_DeliversOwnedFrames(t *testing.T) {
	sess, sender := newCleanAudioTestSession(t, 8)
	ch := sess.CleanAudio()
	if ch == nil {
		t.Fatalf("expected non-nil channel when CleanAudioBufferSize > 0")
	}

	sendSilentPCMUPackets(t, sender, 6)

	var frames []CleanAudioFrame
	deadline := time.After(1 * time.Second)
collect:
	for len(frames) < 2 {
		select {
		case f := <-ch:
			frames = append(frames, f)
		case <-deadline:
			break collect
		}
	}

	if len(frames) < 2 {
		t.Fatalf("expected at least 2 clean-audio frames, got %d", len(frames))
	}
	for i, f := range frames {
		if len(f.PCM) == 0 {
			t.Errorf("frame %d: expected non-empty PCM", i)
		}
	}
	// Ownership check: the two frames must not share a backing array --
	// each call to handlePacket must copy() into a fresh slice, since the
	// pooled cleanPCM buffer backing frames[0] is reused/mutated by the
	// time frames[1] is produced.
	if len(frames[0].PCM) > 0 && len(frames[1].PCM) > 0 {
		if &frames[0].PCM[0] == &frames[1].PCM[0] {
			t.Errorf("frames[0] and frames[1] share a backing array -- clean audio frames must be owned copies")
		}
	}
}

// TestCleanAudio_DropOldestOnFullNeverBlocksHotPath verifies the documented
// backpressure policy: if a consumer never drains the channel, handlePacket
// must keep making progress (dropping old frames) rather than blocking the
// RTP receive loop.
func TestCleanAudio_DropOldestOnFullNeverBlocksHotPath(t *testing.T) {
	sess, sender := newCleanAudioTestSession(t, 1) // tiny buffer, intentionally never drained

	done := make(chan struct{})
	go func() {
		sendSilentPCMUPackets(t, sender, 10)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("sending timed out -- handlePacket appears to have blocked on a full, undrained CleanAudio channel")
	}

	time.Sleep(150 * time.Millisecond)
	if stats := sess.Stats(); stats.PacketsReceived == 0 {
		t.Errorf("expected PacketsReceived > 0 even with an undrained, full CleanAudio channel")
	}
	// Exactly one (the most recent) frame should be sitting in the buffer.
	select {
	case f := <-sess.CleanAudio():
		if len(f.PCM) == 0 {
			t.Errorf("expected the retained frame to have non-empty PCM")
		}
	default:
		t.Errorf("expected one buffered frame to remain available after packets were dropped due to backpressure")
	}
}

// TestCleanAudio_ClosedOnStop verifies Session.Stop() closes the CleanAudio
// channel once the receive loop has fully exited, so consumers ranging over
// it terminate cleanly instead of hanging forever.
func TestCleanAudio_ClosedOnStop(t *testing.T) {
	sess, sender := newCleanAudioTestSession(t, 4)
	ch := sess.CleanAudio()

	sendSilentPCMUPackets(t, sender, 3)
	time.Sleep(100 * time.Millisecond)

	sess.Stop()

	// Drain whatever was buffered, then expect a closed-channel read (ok == false).
	deadline := time.After(1 * time.Second)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return // channel closed as expected
			}
			_ = f
		case <-deadline:
			t.Fatalf("CleanAudio channel was not closed within deadline after Stop()")
		}
	}
}
