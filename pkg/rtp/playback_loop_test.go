package rtp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// TestSession_StartPlaybackLoop_DeliversFramesOverUDP drives the playback
// loop end-to-end through its real public entry points: InjectBotAudio()
// queues encoded frames, and the loop started by Session.Start() (see
// session.go's Start method, which launches startPlaybackLoop as of this
// change) pops them on its 20ms ticker and writes real RTP packets to the
// configured forward address. This also verifies that Start() actually
// wires up the playback loop -- previously it did not, so InjectBotAudio
// had no effect on the wire.
func TestSession_StartPlaybackLoop_DeliversFramesOverUDP(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	const frameCount = 3
	if !sess.InjectBotAudio(zeroPCM(frameCount * 160)) {
		t.Fatal("InjectBotAudio: expected all frames accepted")
	}

	if err := sink.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)

	var lastSeq uint16
	var lastTS uint32
	var ssrc uint32
	for i := 0; i < frameCount; i++ {
		n, err := sink.Read(buf)
		if err != nil {
			t.Fatalf("packet %d: read from sink: %v", i, err)
		}
		hdr, payload, err := parseRTPHeader(buf[:n])
		if err != nil {
			t.Fatalf("packet %d: parseRTPHeader: %v", i, err)
		}
		if hdr.Version != 2 {
			t.Errorf("packet %d: Version = %d, want 2", i, hdr.Version)
		}
		if hdr.PayloadType != sess.cfg.PayloadType {
			t.Errorf("packet %d: PayloadType = %d, want %d", i, hdr.PayloadType, sess.cfg.PayloadType)
		}
		if len(payload) != 160 {
			t.Errorf("packet %d: payload len = %d, want 160", i, len(payload))
		}

		if i == 0 {
			ssrc = hdr.SSRC
			lastSeq = hdr.SequenceNumber
			lastTS = hdr.Timestamp
			continue
		}
		if hdr.SSRC != ssrc {
			t.Errorf("packet %d: SSRC changed mid-stream: %d != %d", i, hdr.SSRC, ssrc)
		}
		if hdr.SequenceNumber != lastSeq+1 {
			t.Errorf("packet %d: SequenceNumber = %d, want %d", i, hdr.SequenceNumber, lastSeq+1)
		}
		if hdr.Timestamp != lastTS+160 {
			t.Errorf("packet %d: Timestamp = %d, want %d", i, hdr.Timestamp, lastTS+160)
		}
		lastSeq = hdr.SequenceNumber
		lastTS = hdr.Timestamp
	}
}

// TestSession_StartPlaybackLoop_AdvancesTimestampOnIdleTicks exercises the
// "nothing queued this tick" branch of startPlaybackLoop: the RTP timestamp
// must keep advancing every 20ms tick even while the playback queue is
// empty, so that audio injected later stays in sync with wall-clock time
// instead of resetting to a fresh epoch.
func TestSession_StartPlaybackLoop_AdvancesTimestampOnIdleTicks(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	// Let several idle ticks (20ms each) elapse with an empty queue before
	// injecting anything.
	time.Sleep(70 * time.Millisecond)

	if !sess.InjectBotAudio(zeroPCM(160)) {
		t.Fatal("InjectBotAudio: expected frame accepted")
	}

	if err := sink.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, err := sink.Read(buf)
	if err != nil {
		t.Fatalf("read from sink: %v", err)
	}
	hdr, _, err := parseRTPHeader(buf[:n])
	if err != nil {
		t.Fatalf("parseRTPHeader: %v", err)
	}

	// At least one full idle tick must have advanced ts before our frame was
	// ever popped, so the timestamp on the first delivered packet should be
	// comfortably ahead of a single frame's worth of samples (160).
	const tsStep = 160
	if hdr.Timestamp < tsStep {
		t.Errorf("Timestamp = %d, want >= %d (idle ticks should advance ts)", hdr.Timestamp, tsStep)
	}
}

// newUnstartedPlaybackSession builds a Session (and its UDP sink) without
// calling Start(), so tests can drive startPlaybackLoop directly and
// deterministically without racing the receive/statsLoop/RTCP goroutines.
func newUnstartedPlaybackSession(t *testing.T) (*net.UDPConn, *Session) {
	t.Helper()
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0,
		JitterDepth: 1,
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
	}
	sess, err := NewSession(cfg)
	if err != nil {
		sink.Close()
		t.Fatalf("NewSession: %v", err)
	}
	return sink, sess
}

// TestStartPlaybackLoop_ExitsOnContextCancel exercises the ctx.Done() exit
// path directly (same-package unexported call, per package convention).
func TestStartPlaybackLoop_ExitsOnContextCancel(t *testing.T) {
	sink, sess := newUnstartedPlaybackSession(t)
	defer sink.Close()
	defer sess.conn.Close()

	ctx, cancel := context.WithCancel(context.Background())

	loopDone := make(chan struct{})
	go func() {
		sess.startPlaybackLoop(ctx)
		close(loopDone)
	}()

	// Give the loop a moment to start and enter its select before cancelling.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-loopDone:
	case <-time.After(1 * time.Second):
		t.Fatal("startPlaybackLoop did not exit after context cancellation")
	}
}

// TestStartPlaybackLoop_ExitsWhenConnClosed exercises the "conn closed"
// silent-exit branch: once s.conn is closed, WriteToUDP fails and the loop
// must return even though ctx is still live.
func TestStartPlaybackLoop_ExitsWhenConnClosed(t *testing.T) {
	sink, sess := newUnstartedPlaybackSession(t)
	defer sink.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopDone := make(chan struct{})
	go func() {
		sess.startPlaybackLoop(ctx)
		close(loopDone)
	}()

	// Queue a frame so the loop attempts (and fails) a real write once the
	// conn is closed.
	sess.InjectBotAudio(zeroPCM(160))
	sess.conn.Close()

	select {
	case <-loopDone:
	case <-time.After(1 * time.Second):
		t.Fatal("startPlaybackLoop did not exit after conn was closed")
	}
}
