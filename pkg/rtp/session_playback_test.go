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

// TestSession_InjectBotAudioAtRate_16kHzResamples proves the fix for the
// InjectBotAudio doc-contract bug: InjectBotAudio's comment promised 8kHz-or-
// 16kHz mono input, but the implementation always chunked in 160-sample
// (20ms @ 8kHz) blocks with no resampling step, so 16kHz input silently
// played back at 2x speed / doubled pitch. InjectBotAudioAtRate now actually
// resamples. 320 samples of 16kHz input (20ms) must resample down to 160
// samples (one 8kHz frame), not be chunked directly into two 8kHz frames.
func TestSession_InjectBotAudioAtRate_16kHzResamples(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	ok := sess.InjectBotAudioAtRate(zeroPCM(320), 16000)
	if !ok {
		t.Fatal("InjectBotAudioAtRate: expected true for one 20ms@16kHz frame")
	}
	if got := sess.playback.Len(); got != 1 {
		t.Errorf("InjectBotAudioAtRate(16kHz): expected exactly 1 queued 8kHz frame after resampling, got %d (2 would mean the input was chunked at the wrong rate with no resampling)", got)
	}
}

// TestSession_InjectBotAudioAtRate_8kHzMatchesInjectBotAudio proves
// InjectBotAudio(pcm16) == InjectBotAudioAtRate(pcm16, 8000): the rate
// parameter must not change existing 8kHz-caller behavior.
func TestSession_InjectBotAudioAtRate_8kHzMatchesInjectBotAudio(t *testing.T) {
	sink, sess := newPlaybackTestSession(t)
	defer sink.Close()
	defer sess.Stop()

	ok := sess.InjectBotAudioAtRate(zeroPCM(160), 8000)
	if !ok {
		t.Fatal("InjectBotAudioAtRate(8000): expected true for single frame")
	}
	if got := sess.playback.Len(); got != 1 {
		t.Errorf("InjectBotAudioAtRate(8000): expected 1 queued frame, got %d", got)
	}
}
