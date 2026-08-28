package rtp

import "testing"

// TestJitterBufferDuplicatePacketDoesNotWedge reproduces a real bug: a
// duplicated UDP datagram (same seq as a packet already sitting in the
// buffer, unpopped) used to be inserted as a second entry. Once the
// original was popped and nextSeq advanced past that seq, the leftover
// duplicate -- now behind nextSeq -- was stuck at buf[0] forever, because
// Pop()'s gap branch advances nextSeq without ever removing a mismatched
// head entry. Every subsequent Pop() call then reported spurious loss
// indefinitely, even though legitimate packets kept arriving right behind
// it. This test pushes a duplicate of an already-buffered (not yet popped)
// packet, then proves the buffer keeps delivering real packets afterward
// instead of wedging on permanent artificial loss.
func TestJitterBufferDuplicatePacketDoesNotWedge(t *testing.T) {
	j := NewJitterBuffer(2)

	// Prime the buffer (depth=2): seq 100, then 101.
	if ready := j.Push(100, 1600, []byte{0xAA}); ready {
		t.Fatalf("expected not ready after first push")
	}
	if ready := j.Push(101, 1760, []byte{0xBB}); !ready {
		t.Fatalf("expected ready after second push")
	}

	// Duplicate of seq 100, which is still buffered (not yet popped).
	// Before the fix this was inserted as a second entry with seq==100.
	j.Push(100, 1600, []byte{0xAA})

	if got := len(j.buf); got != 2 {
		t.Fatalf("duplicate packet was inserted into buffer: len(buf)=%d, want 2", got)
	}

	// Pop the original seq-100 entry.
	p, ok := j.Pop()
	if !ok || p == nil || p[0] != 0xAA {
		t.Fatalf("Pop() = %v, %v; want seq-100 payload", p, ok)
	}

	// Push several more real, in-order packets (seq 102, 103, 104...).
	// If the duplicate wedged the buffer, every one of these would surface
	// as spurious loss instead of being delivered.
	for seq := uint16(102); seq <= 105; seq++ {
		j.Push(seq, uint32(seq)*160, []byte{byte(seq)})
	}

	// seq 101 should pop cleanly next (no wedge from the duplicate).
	p, ok = j.Pop()
	if !ok || p == nil || p[0] != 0xBB {
		t.Fatalf("Pop() after duplicate = %v, %v; want seq-101 payload (0xBB), buffer wedged", p, ok)
	}

	// Subsequent pops should deliver the real packets, not manufactured loss.
	for seq := uint16(102); seq <= 105; seq++ {
		p, ok = j.Pop()
		if !ok {
			t.Fatalf("Pop() for seq %d returned not-ok", seq)
		}
		if p == nil {
			t.Fatalf("Pop() for seq %d reported spurious loss (nil payload) -- buffer wedged by duplicate", seq)
		}
		if p[0] != byte(seq) {
			t.Fatalf("Pop() for seq %d returned wrong payload %v", seq, p)
		}
	}
}
