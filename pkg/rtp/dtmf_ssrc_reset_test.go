package rtp

// TestSSRCChangeResetsDTMFDetector closes a real gap in handlePacket's SSRC-change
// branch (session.go): DTMFDetector.Reset() is documented ("Reset clears detector
// state (call on new call leg).") as the thing to call when a new call leg starts,
// and an SSRC change is exactly that event -- but the SSRC-change branch only ever
// called s.jitter.Reset() and s.pipeline.Reset(); the only s.dtmf.Reset() call site
// was in Stop(), which is inert since the session is being torn down anyway.
//
// Concretely: DTMFDetector suppresses a telephone-event packet whose (eventCode,
// end) pair matches the previous packet's, to collapse RFC4733's convention of
// sending the end packet three times. Without resetting on SSRC change, that
// dedup state leaks across call legs: if the new leg's first DTMF packet happens
// to carry the same event code/end-bit as the old leg's last DTMF packet, it is
// misclassified as a duplicate retransmission and silently dropped -- losing the
// first DTMF digit of the new call.
import (
	"net"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

func TestSSRCChangeResetsDTMFDetector(t *testing.T) {
	sinkConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("bind sink: %v", err)
	}
	defer sinkConn.Close()

	var digits []DTMFDigit
	logger, _ := zap.NewDevelopment()
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sinkConn.LocalAddr().String(),
		PayloadType: 0, // PCMU
		JitterDepth: 1, // prime/pop on every single packet, no buffering delay
		Logger:      logger,
		Suppressor:  model.NewMockSuppressor(),
		OnDTMF:      func(d DTMFDigit) { digits = append(digits, d) },
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.conn.Close()

	const ssrcA uint32 = 0xAAAA0001
	const ssrcB uint32 = 0xBBBB0002
	audioPayload := make([]byte, 160)
	for i := range audioPayload {
		audioPayload[i] = 0xFF // mu-law silence
	}

	// 1. Audio packet on leg A: establishes currentSSRC/ssrcSet.
	if err := sess.handlePacket(buildRawRTPPacket(1, 0, ssrcA, audioPayload)); err != nil {
		t.Fatalf("handlePacket audio A: %v", err)
	}

	// 2. DTMF digit "5", end packet, on leg A.
	if err := sess.handlePacket(buildDTMFRTPPacket(2, 160, ssrcA, 5, true, 10, 800)); err != nil {
		t.Fatalf("handlePacket dtmf A: %v", err)
	}
	if len(digits) != 1 {
		t.Fatalf("after leg A digit: want 1 digit delivered, got %d", len(digits))
	}
	if digits[0].Digit != "5" {
		t.Fatalf("leg A digit: want %q, got %q", "5", digits[0].Digit)
	}

	// 3. Audio packet on leg B (different SSRC): triggers the SSRC-change
	// branch, which must reset the DTMF detector along with jitter/pipeline.
	if err := sess.handlePacket(buildRawRTPPacket(3, 320, ssrcB, audioPayload)); err != nil {
		t.Fatalf("handlePacket audio B: %v", err)
	}
	if sess.currentSSRC != ssrcB || !sess.ssrcSet {
		t.Fatalf("SSRC change not tracked: currentSSRC=0x%08X ssrcSet=%v", sess.currentSSRC, sess.ssrcSet)
	}

	// 4. DTMF digit "5", end packet, on leg B -- identical (eventCode, end)
	// to step 2. If the detector's dedup state survived the SSRC change,
	// this is wrongly treated as a duplicate of leg A's packet and dropped.
	if err := sess.handlePacket(buildDTMFRTPPacket(4, 320, ssrcB, 5, true, 10, 800)); err != nil {
		t.Fatalf("handlePacket dtmf B: %v", err)
	}
	if len(digits) != 2 {
		t.Fatalf("after leg B digit: want 2 digits delivered (detector must reset on SSRC change), got %d", len(digits))
	}
	if digits[1].Digit != "5" {
		t.Fatalf("leg B digit: want %q, got %q", "5", digits[1].Digit)
	}
}
