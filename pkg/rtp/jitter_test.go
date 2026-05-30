package rtp

import (
	"testing"
)

func push(jb *JitterBuffer, seq uint16, payload []byte) {
	jb.Push(seq, uint32(seq)*160, payload)
}

func TestJitterInOrder(t *testing.T) {
	jb := NewJitterBuffer(2)
	push(jb, 0, []byte{0})
	push(jb, 1, []byte{1})

	p0, ok0 := jb.Pop()
	if !ok0 || p0 == nil || p0[0] != 0 {
		t.Errorf("expected seq 0, got ok=%v payload=%v", ok0, p0)
	}
	p1, ok1 := jb.Pop()
	if !ok1 || p1 == nil || p1[0] != 1 {
		t.Errorf("expected seq 1, got ok=%v payload=%v", ok1, p1)
	}
}

func TestJitterOutOfOrder(t *testing.T) {
	jb := NewJitterBuffer(2)
	// Push out of order: 1, 0, 2
	push(jb, 1, []byte{1})
	push(jb, 0, []byte{0})
	push(jb, 2, []byte{2})

	p, ok := jb.Pop()
	if !ok || p == nil || p[0] != 0 {
		t.Errorf("expected seq 0 first, got ok=%v payload=%v", ok, p)
	}
}

func TestJitterPacketLoss(t *testing.T) {
	jb := NewJitterBuffer(2)
	push(jb, 0, []byte{0})
	push(jb, 2, []byte{2}) // seq 1 is missing

	// Pop seq 0
	p0, _ := jb.Pop()
	if p0 == nil || p0[0] != 0 {
		t.Errorf("expected seq 0, got %v", p0)
	}
	// Pop seq 1 (lost) - should return nil payload, ok=true
	p1, ok1 := jb.Pop()
	if !ok1 {
		t.Error("expected ok=true for lost packet")
	}
	if p1 != nil {
		t.Errorf("expected nil payload for lost packet, got %v", p1)
	}
}

func TestJitterSeqWraparound(t *testing.T) {
	jb := NewJitterBuffer(2)
	// Simulate wraparound: 65534 -> 65535 -> 0 -> 1
	push(jb, 65534, []byte{254})
	push(jb, 65535, []byte{255})
	push(jb, 0, []byte{0})
	push(jb, 1, []byte{1})

	p, ok := jb.Pop()
	if !ok || p == nil || p[0] != 254 {
		t.Errorf("expected 65534 first, got ok=%v payload=%v", ok, p)
	}
}

func TestJitterReset(t *testing.T) {
	jb := NewJitterBuffer(2)
	push(jb, 0, []byte{0})
	push(jb, 1, []byte{1})
	jb.Reset()

	_, ok := jb.Pop()
	if ok {
		t.Error("expected ok=false after reset")
	}
}

func TestJitterNotPrimedUntilDepth(t *testing.T) {
	jb := NewJitterBuffer(3) // needs 3 packets before primed
	push(jb, 0, []byte{0})
	push(jb, 1, []byte{1})

	_, ok := jb.Pop()
	if ok {
		t.Error("buffer should not be primed with only 2 packets (depth=3)")
	}

	push(jb, 2, []byte{2})
	_, ok = jb.Pop()
	if !ok {
		t.Error("buffer should be primed with 3 packets")
	}
}
