package rtp

import (
	"math"
	"testing"
)

func TestParseRTCPReceiverReport(t *testing.T) {
	// Craft a minimal RTCP RR packet
	// V=2, P=0, RC=1, PT=201, length=7
	pkt := []byte{
		0x81, 0xC9, 0x00, 0x07, // header: V=2,RC=1,PT=201,len=7
		0x00, 0x00, 0x00, 0x01, // sender SSRC
		// Report block:
		0xDE, 0xAD, 0xBE, 0xEF, // SSRC of source
		0x80, 0x00, 0x00, 0x05, // fraction_lost=128/256=0.5, cumulative=5
		0x00, 0x01, 0x00, 0x00, // extended highest seq
		0x00, 0x00, 0x00, 0x0A, // jitter = 10
		0x00, 0x00, 0x00, 0x00, // last SR
		0x00, 0x00, 0x00, 0x00, // delay since last SR
	}

	rr, err := ParseRTCPReceiverReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if rr == nil {
		t.Fatal("expected RR, got nil")
	}

	if math.Abs(rr.FractionLost-0.5) > 0.01 {
		t.Errorf("fraction lost: got %.2f, want 0.50", rr.FractionLost)
	}
	if rr.CumulativeLost != 5 {
		t.Errorf("cumulative lost: got %d, want 5", rr.CumulativeLost)
	}
	if rr.Jitter != 10 {
		t.Errorf("jitter: got %d, want 10", rr.Jitter)
	}
}

func TestParseRTCPTooShort(t *testing.T) {
	_, err := ParseRTCPReceiverReport([]byte{0x81, 0xC9})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestParseRTCPWrongType(t *testing.T) {
	// PT=200 (Sender Report) — should return nil, nil
	pkt := make([]byte, 32)
	pkt[0] = 0x81
	pkt[1] = 0xC8 // PT=200 SR
	rr, err := ParseRTCPReceiverReport(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if rr != nil {
		t.Error("expected nil for SR packet")
	}
}

func TestPLCFadeToSilence_RTCPBasic(t *testing.T) {
	jb := NewJitterBuffer(4)

	// Seed lastGoodFrame directly via onGoodPacket (simulates a received frame)
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = 10000
	}
	jb.onGoodPacket(frame)

	// Generate PLC frames — each should be quieter than the previous
	plc1 := jb.generatePLC()
	plc2 := jb.generatePLC()
	plc3 := jb.generatePLC()

	energy := func(f []int16) float64 {
		var sum float64
		for _, s := range f {
			sum += float64(s) * float64(s)
		}
		return sum
	}

	if energy(plc1) <= energy(plc2) {
		t.Error("PLC frame 1 should be louder than frame 2 (fade to silence)")
	}
	if energy(plc2) <= energy(plc3) {
		t.Error("PLC frame 2 should be louder than frame 3 (fade to silence)")
	}
}
