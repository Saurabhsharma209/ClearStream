package rtp

import (
	"net"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

func newPlaybackTestSession(t *testing.T) (*net.UDPConn, *Session) {
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
	sess.Start()
	return sink, sess
}

func zeroPCM(samples int) []byte {
	return make([]byte, samples*2)
}

func TestSession_InjectBotAudio_SingleFrame(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	ok := sess.InjectBotAudio(zeroPCM(160))
	if !ok {
		t.Error("InjectBotAudio returned false for single frame")
	}
	stats := sess.PlaybackStats()
	if stats.Pushed != 1 {
		t.Errorf("Pushed: want 1, got %d", stats.Pushed)
	}
}

func TestSession_InjectBotAudio_MultiFrame(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	ok := sess.InjectBotAudio(zeroPCM(3*160 + 80))
	if !ok {
		t.Error("InjectBotAudio returned false for multi-frame input")
	}
	stats := sess.PlaybackStats()
	if stats.Pushed != 4 {
		t.Errorf("Pushed: want 4 (3 full + 1 padded), got %d", stats.Pushed)
	}
}

func TestSession_ClearPlayback(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.InjectBotAudio(zeroPCM(3 * 160))
	if sess.playback.Len() != 3 {
		t.Fatalf("pre-clear Len: want 3, got %d", sess.playback.Len())
	}

	n := sess.ClearPlayback()
	if n != 3 {
		t.Errorf("ClearPlayback: want 3 discarded, got %d", n)
	}
	if sess.playback.Len() != 0 {
		t.Errorf("post-clear Len: want 0, got %d", sess.playback.Len())
	}
}

func TestSession_PlaybackStats_Counters(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	sess.InjectBotAudio(zeroPCM(2 * 160))
	sess.ClearPlayback()

	stats := sess.PlaybackStats()
	if stats.Pushed != 2 {
		t.Errorf("Pushed: want 2, got %d", stats.Pushed)
	}
	if stats.Cleared == 0 {
		t.Errorf("Cleared: want >0 after ClearPlayback, got %d", stats.Cleared)
	}
}
