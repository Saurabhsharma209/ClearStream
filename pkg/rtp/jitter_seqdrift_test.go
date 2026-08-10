package rtp

import "testing"

// TestJitterBufferLargeForwardDriftResyncs verifies that a huge forward
// sequence-number jump (e.g. caused by a mid-call codec renegotiation or SIP
// re-INVITE that restarts RTP sequence numbering without changing the SSRC)
// is treated as a stream discontinuity and resynced immediately, rather than
// being tail-chased one sequence number at a time (which would otherwise
// manifest as thousands of spurious packet-loss/PLC events).
func TestJitterBufferLargeForwardDriftResyncs(t *testing.T) {
	jb := NewJitterBuffer(2)

	// Prime normally and consume the first packet so nextSeq advances to 1.
	push(jb, 0, []byte{0})
	push(jb, 1, []byte{1})
	p0, ok0 := jb.Pop()
	if !ok0 || p0 == nil || p0[0] != 0 {
		t.Fatalf("setup: expected seq 0 first, got ok=%v payload=%v", ok0, p0)
	}
	// nextSeq is now 1; the buffer still holds the unpopped seq-1 entry.

	// Now the sender jumps far ahead -- a gap of 1000, way beyond
	// maxSeqDrift (500). This should trigger an immediate resync: the stale
	// seq-1 entry is discarded and the buffer re-primes around the new
	// sequence range instead of reporting ~1000 lost packets.
	const jumpSeq = 1001 // fwd = 1001 - 1 = 1000 > maxSeqDrift
	push(jb, jumpSeq, []byte{7})
	push(jb, jumpSeq+1, []byte{8}) // second packet needed to re-prime (depth=2)

	p, ok := jb.Pop()
	if !ok {
		t.Fatalf("expected ok=true after drift resync")
	}
	if p == nil {
		t.Fatalf("expected an immediate real payload after drift resync, got a loss/PLC placeholder (nil) -- drift detection did not resync")
	}
	if p[0] != 7 {
		t.Errorf("expected resynced payload for seq %d (byte 7), got %v", jumpSeq, p)
	}

	p2, ok2 := jb.Pop()
	if !ok2 || p2 == nil || p2[0] != 8 {
		t.Errorf("expected second resynced payload (byte 8), got ok=%v payload=%v", ok2, p2)
	}
}

// TestJitterBufferLargeBackwardDriftResyncs verifies that a huge backward
// sequence-number jump (sender restarts numbering at a much lower value,
// without an SSRC change) is also treated as a discontinuity and resynced,
// rather than leaving the buffer stuck waiting for nextSeq to wrap all the
// way back around (tens of thousands of Pop() calls later).
func TestJitterBufferLargeBackwardDriftResyncs(t *testing.T) {
	jb := NewJitterBuffer(2)

	push(jb, 999, []byte{99})
	push(jb, 1000, []byte{100})
	p0, ok0 := jb.Pop()
	if !ok0 || p0 == nil || p0[0] != 99 {
		t.Fatalf("setup: expected seq 999 first, got ok=%v payload=%v", ok0, p0)
	}
	// nextSeq is now 1000.

	// Sender restarts numbering at 300: bwd = 1000 - 300 = 700 > maxSeqDrift.
	const restartSeq = 300
	push(jb, restartSeq, []byte{3})
	push(jb, restartSeq+1, []byte{4})

	p, ok := jb.Pop()
	if !ok {
		t.Fatalf("expected ok=true after backward drift resync")
	}
	if p == nil {
		t.Fatalf("expected an immediate real payload after backward drift resync, got a loss/PLC placeholder (nil)")
	}
	if p[0] != 3 {
		t.Errorf("expected resynced payload for seq %d (byte 3), got %v", restartSeq, p)
	}
}

// TestJitterBufferSmallGapStillTreatedAsLoss is a regression guard: an
// ordinary, plausible packet-loss gap (well under maxSeqDrift) must continue
// to be reported via the normal Pop() loss/PLC path, not be misidentified as
// a stream reset.
func TestJitterBufferSmallGapStillTreatedAsLoss(t *testing.T) {
	jb := NewJitterBuffer(2)

	push(jb, 0, []byte{0})
	push(jb, 1, []byte{1})
	p0, ok0 := jb.Pop()
	if !ok0 || p0 == nil || p0[0] != 0 {
		t.Fatalf("setup: expected seq 0 first, got ok=%v payload=%v", ok0, p0)
	}
	// nextSeq is now 1. Push a gap of 50 -- comfortably under maxSeqDrift (500).
	const gapSeq = 51
	push(jb, gapSeq, []byte{51})

	// The still-buffered seq-1 entry pops first (it matches nextSeq exactly,
	// no loss yet).
	p1, ok1 := jb.Pop()
	if !ok1 || p1 == nil || p1[0] != 1 {
		t.Fatalf("expected seq 1 (already buffered) to pop cleanly first, got ok=%v payload=%v", ok1, p1)
	}

	lossCount := 0
	var got []byte
	for {
		p, ok := jb.Pop()
		if !ok {
			break
		}
		if p == nil {
			lossCount++
			continue
		}
		got = p
		break
	}

	if lossCount == 0 {
		t.Errorf("expected ordinary loss reporting for a small gap, got no loss frames at all")
	}
	if got == nil || got[0] != 51 {
		t.Errorf("expected to eventually reach seq %d payload, got %v (loss frames seen=%d)", gapSeq, got, lossCount)
	}
}

// TestJitterBufferWraparoundSmallGapNoFalseDrift confirms that a small,
// legitimate forward distance computed across the 16-bit wraparound boundary
// (e.g. 65500 -> 50, a plausible ~86-packet gap) is NOT misidentified as a
// large drift/reset -- only genuinely large discontinuities should resync.
func TestJitterBufferWraparoundSmallGapNoFalseDrift(t *testing.T) {
	jb := NewJitterBuffer(2)

	push(jb, 65499, []byte{1})
	push(jb, 65500, []byte{2})
	p0, ok0 := jb.Pop()
	if !ok0 || p0 == nil || p0[0] != 1 {
		t.Fatalf("setup: expected seq 65499 first, got ok=%v payload=%v", ok0, p0)
	}
	// nextSeq is now 65500. Push seq 50, wrapping around: forward distance
	// (65500 -> 50) is 65536-65500+50 = 86, well under maxSeqDrift (500).
	push(jb, 50, []byte{50})

	// Drain any (plausible, expected) loss frames from the ~85-packet gap
	// and confirm we eventually reach seq 50's real payload rather than the
	// buffer being wiped/resynced around it.
	found := false
	for i := 0; i < 200; i++ {
		p, ok := jb.Pop()
		if !ok {
			break
		}
		if p != nil && p[0] == 50 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to reach seq 50's payload via normal loss catch-up, not a drift resync")
	}
}

// TestJitterBufferStaleDuplicateIsDropped verifies that a packet whose
// sequence number is behind nextSeq (i.e. already delivered, or already
// skipped past as lost) is rejected by Push() rather than being inserted
// into the buffer. This is a small backward gap -- well under maxSeqDrift --
// so it must NOT be treated as a stream reset either; it is simply a stale
// straggler (duplicate UDP datagram, upstream retransmit, or a packet
// reordered so far behind its peers that Pop() already moved past it) and
// should be silently discarded.
func TestJitterBufferStaleDuplicateIsDropped(t *testing.T) {
	jb := NewJitterBuffer(2)
	push(jb, 10, []byte{10})
	push(jb, 11, []byte{11})
	if p, ok := jb.Pop(); !ok || p == nil || p[0] != 10 {
		t.Fatalf("setup: expected seq 10 first, got ok=%v payload=%v", ok, p)
	}
	if p, ok := jb.Pop(); !ok || p == nil || p[0] != 11 {
		t.Fatalf("setup: expected seq 11 second, got ok=%v payload=%v", ok, p)
	}
	// nextSeq is now 12, buffer is empty.

	// A duplicate/retransmitted copy of the already-delivered seq-10 packet
	// arrives late.
	push(jb, 10, []byte{99})
	if len(jb.buf) != 0 {
		t.Fatalf("expected stale duplicate seq 10 to be dropped without buffering, got buf=%v", jb.buf)
	}
}

// TestJitterBufferStaleDuplicateDoesNotWedgeBuffer is a regression test for a
// real production bug: Pop()'s gap-handling path only ever inspects buf[0]
// and, on a sequence mismatch, advances nextSeq WITHOUT removing the
// offending head entry (that's what lets a legitimately late packet "catch
// up" to nextSeq after ordinary loss). Before the fix in Push(), a stale
// packet with seq behind nextSeq (a duplicate/retransmission, or a very late
// arrival for a sequence number already counted as lost) was inserted into
// the buffer like any other packet. Because its seq could never again equal
// nextSeq until the entire 16-bit sequence space wrapped around
// (~65536 Pop() calls later), it would permanently wedge the head of the
// buffer: every subsequent Pop() reports spurious loss/PLC, and every
// legitimately-arrived packet queued behind it gets evicted by the
// maxDepth tail-drop instead of ever being played out.
func TestJitterBufferStaleDuplicateDoesNotWedgeBuffer(t *testing.T) {
	jb := NewJitterBuffer(2)
	push(jb, 10, []byte{10})
	push(jb, 11, []byte{11})
	jb.Pop() // consumes seq 10
	jb.Pop() // consumes seq 11; nextSeq is now 12, buffer empty

	// Stale duplicate of an already-delivered packet arrives late.
	push(jb, 10, []byte{99})

	// A legitimate new packet arrives right after.
	const wantSeq = 15
	push(jb, wantSeq, []byte{byte(wantSeq)})

	// Confirm we reach the real seq-15 payload within a handful of ordinary
	// loss frames (seq 12, 13, 14) instead of the buffer being wedged
	// indefinitely by the stale seq-10 entry.
	const maxIterations = 20
	lossCount := 0
	var got []byte
	for i := 0; i < maxIterations; i++ {
		p, ok := jb.Pop()
		if !ok {
			break
		}
		if p == nil {
			lossCount++
			continue
		}
		got = p
		break
	}

	if got == nil || got[0] != byte(wantSeq) {
		t.Fatalf("expected to reach seq %d's payload within %d Pop() calls (a stale duplicate must not wedge the buffer), got payload=%v after %d loss frames", wantSeq, maxIterations, got, lossCount)
	}
	if lossCount != 3 {
		t.Errorf("expected exactly 3 ordinary loss frames (seq 12,13,14) before reaching seq %d, got %d", wantSeq, lossCount)
	}
}
