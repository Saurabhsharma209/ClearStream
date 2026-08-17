package rtp

import (
	"math"
	"net"
	"strings"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// TestSession_NoDiarizer_CurrentSpeakerUnknownAndReportUnaffected is the
// zero-cost-when-unused half of the Diarizer wiring: a Session built with no
// Config.Diarizer must report audio.SpeakerUnknown from CurrentSpeaker, have
// a nil DiarizationSegments, and must NOT append a "Speaker:" field to
// QualityReport -- so the report format is unchanged for the large majority
// of sessions that never configure diarization.
func TestSession_NoDiarizer_CurrentSpeakerUnknownAndReportUnaffected(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sink.Close()

	logger, _ := zap.NewDevelopment()
	sess, err := NewSession(Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0,
		Logger:      logger,
		Suppressor:  model.NewPassthrough(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	if got := sess.CurrentSpeaker(); got != audio.SpeakerUnknown {
		t.Errorf("CurrentSpeaker() with no Diarizer configured: got %s, want %s", got, audio.SpeakerUnknown)
	}
	if segs := sess.DiarizationSegments(); segs != nil {
		t.Errorf("DiarizationSegments() with no Diarizer configured: got %v, want nil", segs)
	}
	if report := sess.QualityReport(); strings.Contains(report, "Speaker:") {
		t.Errorf("QualityReport() with no Diarizer configured must not contain a Speaker field, got: %s", report)
	}
}

// TestSession_WithDiarizer_TracksSpeakerAndReport is the roadmap Phase 2
// ("Multi-speaker awareness") wiring itself: audio.EnergyDiarizer already
// existed and was already fully wired into audio.Pipeline, but pkg/rtp
// (the only package that terminates live telephony calls) never passed a
// Diarizer through from Config, so no live RTP session could ever surface a
// speaker label. This drives a real Session with a loud, non-silent G.711
// signal through Config.Diarizer and confirms CurrentSpeaker reflects the
// active speaker and QualityReport surfaces it.
func TestSession_WithDiarizer_TracksSpeakerAndReport(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sink.Close()

	logger, _ := zap.NewDevelopment()
	sess, err := NewSession(Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0,
		JitterDepth: 1, // process each packet immediately instead of buffering for reorder
		Logger:      logger,
		Suppressor:  model.NewPassthrough(),
		Diarizer:    audio.NewEnergyDiarizer(audio.DefaultEnergyDiarizerConfig()),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	// A configured Diarizer disqualifies the session from bypass mode (see
	// Pipeline.IsBypass), since diarization needs the decoded PCM the bypass
	// path deliberately skips.
	if sess.isBypassMode() {
		t.Fatal("expected non-bypass mode with a Diarizer configured")
	}

	// EnergyDiarizer initializes its current (ongoing) segment as
	// SpeakerSilence at construction time (see NewEnergyDiarizer) -- unlike
	// the no-Diarizer-configured case, which reports SpeakerUnknown, a
	// configured-but-not-yet-fed diarizer has a real, defined "nobody is
	// talking yet" state to report.
	if got := sess.CurrentSpeaker(); got != audio.SpeakerSilence {
		t.Errorf("CurrentSpeaker() before any packet: got %s, want %s", got, audio.SpeakerSilence)
	}

	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(12000 * math.Sin(2*math.Pi*float64(i)/20.0))
	}
	loudPayload := encodeG711U(samples)
	pkt := buildRawRTPPacket(0, 0, 0xC0FFEE, loudPayload)
	if err := sess.handlePacket(pkt); err != nil {
		t.Fatalf("handlePacket: %v", err)
	}

	if got := sess.CurrentSpeaker(); got != audio.SpeakerNearEnd {
		t.Errorf("CurrentSpeaker() after loud frame: got %s, want %s", got, audio.SpeakerNearEnd)
	}

	report := sess.QualityReport()
	if !strings.Contains(report, "Speaker: near") {
		t.Errorf("QualityReport() with Diarizer configured must contain 'Speaker: near', got: %s", report)
	}
}
