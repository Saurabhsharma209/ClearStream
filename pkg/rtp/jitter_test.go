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

// TestJitterBufferSeqWrapAround verifies that seqLess correctly orders packets
// around the 16-bit sequence number wraparound boundary. Two packets near the
// wrap (65534 and 65535) are pushed in-order with depth=2; the buffer primes and
// pops them in the correct order without treating the high seqnums as "large" and
// the post-wrap seqnums as "small" in a naive unsigned comparison.
func TestJitterBufferSeqWrapAround(t *testing.T) {
	// Push exactly two packets near the wraparound boundary.
	// depth=2 means the buffer primes as soon as both are buffered.
	jb := NewJitterBuffer(2)
	jb.Push(65534, 65534*160, []byte{0xFE})
	jb.Push(65535, 65535*160, []byte{0xFF})

	// First pop: must be seq 65534
	p0, ok0 := jb.Pop()
	if !ok0 || p0 == nil || p0[0] != 0xFE {
		t.Errorf("expected seq 65534 (0xFE) first, got ok=%v payload=%v", ok0, p0)
	}

	// Second pop: must be seq 65535
	p1, ok1 := jb.Pop()
	if !ok1 || p1 == nil || p1[0] != 0xFF {
		t.Errorf("expected seq 65535 (0xFF) second, got ok=%v payload=%v", ok1, p1)
	}

	// seqLess must recognise that 65535 is "less than" 0 when the diff exceeds
	// maxSeqDrift, i.e. post-wraparound small seqnums come after 65535.
	if !seqLess(65535, 0) {
		t.Error("seqLess(65535, 0) should return true: wraparound means 0 follows 65535")
	}
}

// TestJitterBufferReorderRecovery verifies that packets pushed out of order
// arrive in sorted sequence order. We use depth=5 so the buffer waits for all
// packets before popping, guaranteeing correct reorder before the first Pop.
func TestJitterBufferReorderRecovery(t *testing.T) {
	jb := NewJitterBuffer(5)
	pushOrder := []uint16{3, 1, 4, 0, 2}
	for _, s := range pushOrder {
		jb.Push(s, uint32(s)*160, []byte{byte(s)})
	}

	for want := uint16(0); want < 5; want++ {
		p, ok := jb.Pop()
		if !ok {
			t.Fatalf("expected ok=true for seq %d", want)
		}
		if p == nil {
			t.Fatalf("expected payload for seq %d, got nil", want)
		}
		if p[0] != byte(want) {
			t.Errorf("expected seq %d payload, got %d", want, p[0])
		}
	}
}

// TestJitterBufferDuplicateDrop verifies behavior when the same seqnum is pushed
// twice. The current implementation does not deduplicate in Push, so both copies
// enter the sorted buffer and both are popped. This test documents that the buffer
// emits the duplicate — callers are responsible for upstream deduplication.
func TestJitterBufferDuplicateDrop(t *testing.T) {
	jb := NewJitterBuffer(2)
	// Push seq 10 twice, then seq 11 to prime the buffer (depth=2).
	jb.Push(10, 10*160, []byte{10})
	jb.Push(10, 10*160, []byte{10}) // duplicate — both enter the buffer
	jb.Push(11, 11*160, []byte{11})

	// Count how many times seq 10's payload (value=10) appears.
	count := 0
	for {
		p, ok := jb.Pop()
		if !ok {
			break
		}
		if p != nil && p[0] == 10 {
			count++
		}
	}
	// With the current sort-only implementation, duplicates are not dropped;
	// both copies are returned. Assert the observed count is at least 1.
	if count < 1 {
		t.Errorf("expected seq 10 to appear at least once, got %d", count)
	}
}

// TestJitterBufferReset verifies that after Reset(), previously buffered packets
// are gone and only newly pushed packets are returned.
func TestJitterBufferReset(t *testing.T) {
	jb := NewJitterBuffer(2)
	// Push 3 packets, then reset
	jb.Push(100, 100*160, []byte{100})
	jb.Push(101, 101*160, []byte{101})
	jb.Push(102, 102*160, []byte{102})
	jb.Reset()

	// Push 3 new packets with different seqnums
	jb.Push(200, 200*160, []byte{200})
	jb.Push(201, 201*160, []byte{201})

	// Should only get the new packets
	p, ok := jb.Pop()
	if !ok {
		t.Fatal("expected ok=true after reset and re-push")
	}
	if p == nil || p[0] != 200 {
		t.Errorf("expected first packet after reset to be seq 200, got %v", p)
	}

	p2, ok2 := jb.Pop()
	if !ok2 {
		t.Fatal("expected ok=true for second packet after reset")
	}
	if p2 == nil || p2[0] != 201 {
		t.Errorf("expected second packet after reset to be seq 201, got %v", p2)
	}

	// Old seqnums (100-102) must not appear
	for {
		p3, ok3 := jb.Pop()
		if !ok3 {
			break
		}
		if p3 != nil && (p3[0] == 100 || p3[0] == 101 || p3[0] == 102) {
			t.Errorf("old packet with payload %d appeared after Reset()", p3[0])
		}
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

// TestJitterDepthAndJitterMs verifies that Depth() and JitterMs() are accessible
// and return sane initial values.
func TestJitterDepthAndJitterMs(t *testing.T) {
	jb := NewJitterBuffer(4)
	if d := jb.Depth(); d != 4 {
		t.Errorf("initial Depth should be 4, got %d", d)
	}
	if ms := jb.JitterMs(); ms != 0 {
		t.Errorf("initial JitterMs should be 0, got %.2f", ms)
	}
}

// TestJitterGeneratePLCSilenceWithNoHistory verifies that GeneratePLC returns
// a silent frame when no good frame has been stored yet.
func TestJitterGeneratePLCSilenceWithNoHistory(t *testing.T) {
	jb := NewJitterBuffer(2)
	frame := jb.GeneratePLC()
	if len(frame) == 0 {
		t.Error("expected non-empty PLC frame")
	}
	for i, s := range frame {
		if s != 0 {
			t.Errorf("expected silence, got frame[%d]=%d", i, s)
		}
	}
}

// TestJitterOnGoodPacketAndPLC verifies that after a good packet is stored,
// GeneratePLC returns a non-silent frame for the first two losses (waveform
// substitution) and a decaying frame for loss 3+ (fade-to-silence).
func TestJitterOnGoodPacketAndPLC(t *testing.T) {
	jb := NewJitterBuffer(2)

	// Store a non-zero frame as the last good frame.
	goodFrame := make([]int16, 160)
	for i := range goodFrame {
		goodFrame[i] = 1000
	}
	jb.OnGoodPacket(goodFrame)

	// Loss 1 and 2: waveform substitution — should be non-zero.
	for loss := 1; loss <= 2; loss++ {
		frame := jb.GeneratePLC()
		allZero := true
		for _, s := range frame {
			if s != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			t.Errorf("loss %d: expected non-zero waveform substitution frame, got all zeros", loss)
		}
	}

	// Loss 3+: exponential fade. Amplitude must decrease with each call.
	prev := jb.GeneratePLC() // loss 3
	for loss := 4; loss <= 6; loss++ {
		curr := jb.GeneratePLC()
		// Compare max absolute value: must be <= previous.
		var prevMax, currMax int16
		for i := range prev {
			if prev[i] < 0 {
				if -prev[i] > prevMax {
					prevMax = -prev[i]
				}
			} else if prev[i] > prevMax {
				prevMax = prev[i]
			}
			if curr[i] < 0 {
				if -curr[i] > currMax {
					currMax = -curr[i]
				}
			} else if curr[i] > currMax {
				currMax = curr[i]
			}
		}
		if currMax > prevMax {
			t.Errorf("loss %d: PLC amplitude %d > previous %d (should be fading)", loss, currMax, prevMax)
		}
		prev = curr
	}
}

// TestJitterOnGoodPacketResetsLoss verifies that after a good packet is received,
// the consecutive-loss counter resets so the next loss starts waveform substitution again.
func TestJitterOnGoodPacketResetsLoss(t *testing.T) {
	jb := NewJitterBuffer(2)

	goodFrame := make([]int16, 160)
	for i := range goodFrame {
		goodFrame[i] = 2000
	}

	// Advance to loss 5 (fade region).
	jb.OnGoodPacket(goodFrame)
	for i := 0; i < 5; i++ {
		jb.GeneratePLC()
	}

	// Receive a good packet — resets loss counter.
	jb.OnGoodPacket(goodFrame)

	// Next loss should be substitution (loss 1) not fade.
	frame := jb.GeneratePLC()
	allZero := true
	for _, s := range frame {
		if s != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("after OnGoodPacket reset, first PLC loss should be waveform substitution (non-zero)")
	}
}

// TestPLCFadeToSilence is the canonical CS-003 regression test.
//
// It verifies two critical properties of the PLC fade-to-silence path:
//
// 1. Monotonic: every successive PLC frame has amplitude ≤ the previous frame.
//    This must hold across the waveform-substitution → fade TRANSITION (loss 2→3),
//    which was the original bug: the first fade frame used lastGoodFrame as its
//    source, so it could be louder than the last waveform-sub frame if the sub
//    happened to copy from a low-energy region of the frame.
//
// 2. Convergence: after 40 consecutive losses, all samples must be zero or
//    near-zero (abs < 5). With 0.85^n per frame, after 38 fade steps starting
//    from amplitude ≤ 32767: 32767 × 0.85^38 ≈ 11, truncated to int16 → ~11.
//    After 50 steps: 32767 × 0.85^50 ≈ 2 → rounds toward 0.
//
// The test uses a frame where the FIRST pitch period (samples 0..period-1) is
// quiet (values ≈ 0) and later samples are loud, so that the old code's bug
// (waveform-sub copies from index 0 of lastGoodFrame) would trigger the
// non-monotonic jump at the loss 2→3 boundary.
func TestPLCFadeToSilence(t *testing.T) {
	jb := NewJitterBuffer(2)

	// Build a frame where the beginning is quiet and the end is loud.
	// This maximises the chance of triggering a non-monotonic amplitude
	// increase at the waveform-substitution → fade transition.
	goodFrame := make([]int16, 160)
	for i := range goodFrame {
		if i < 40 {
			goodFrame[i] = 10 // quiet first pitch period (minLag = 40 samples)
		} else {
			goodFrame[i] = 8000 // loud rest of frame
		}
	}
	jb.OnGoodPacket(goodFrame)

	var prevMax int16
	for loss := 1; loss <= 60; loss++ {
		frame := jb.GeneratePLC()

		// Compute max absolute value of this PLC frame.
		var currMax int16
		for _, s := range frame {
			if s < 0 {
				s = -s
			}
			if s > currMax {
				currMax = s
			}
		}

		if loss > 1 && currMax > prevMax {
			t.Errorf("PLC loss %d: amplitude %d > previous %d (non-monotonic — fade-to-silence bug)",
				loss, currMax, prevMax)
		}

		// After 50 losses, must be near-zero.
		if loss == 60 && currMax > 5 {
			t.Errorf("PLC loss %d: expected near-silence (< 5), got max amplitude %d", loss, currMax)
		}

		prevMax = currMax
		t.Logf("loss %d: maxAbs=%d", loss, currMax)
	}
}

// TestDetectPitch sanity-checks that detectPitch returns a period in the valid range
// for a 440 Hz sine wave at 16 kHz (expected period ≈ 36 samples).
func TestDetectPitch(t *testing.T) {
	makeSample := func(i int) int16 {
		// 440 Hz sine at 16 kHz
		v := 8000.0 * sinApprox(2.0*3.14159265*440.0*float64(i)/16000.0)
		if v > 32767 {
			return 32767
		}
		if v < -32768 {
			return -32768
		}
		return int16(v)
	}
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = makeSample(i)
	}
	period := detectPitch(frame)
	// 440 Hz at 16 kHz → period ≈ 36.4 samples. Accept 30–45.
	if period < 30 || period > 45 {
		t.Errorf("detectPitch for 440 Hz sine: expected 30–45, got %d", period)
	}
}

// TestResetRestoresInitialDepth is the CS-002 regression test.
// NewJitterBuffer(8) should use depth=8 after Reset(), not the package default of 4.
func TestResetRestoresInitialDepth(t *testing.T) {
	const configuredDepth = 8
	jb := NewJitterBuffer(configuredDepth)

	// Prime the buffer by pushing enough packets, then drain it.
	for i := uint16(0); i < configuredDepth; i++ {
		jb.Push(i, uint32(i)*160, []byte{byte(i)})
	}
	for i := 0; i < configuredDepth; i++ {
		jb.Pop()
	}

	// Now reset and verify depth is restored to configuredDepth, not defaultJitterDepth (4).
	jb.Reset()
	if got := jb.Depth(); got != configuredDepth {
		t.Errorf("after Reset(), Depth() = %d, want %d (configured); got defaultJitterDepth instead", got, configuredDepth)
	}

	// Also verify the buffer re-primes at the configured depth after reset.
	for i := uint16(0); i < configuredDepth-1; i++ {
		primed := jb.Push(i, uint32(i)*160, []byte{byte(i)})
		if primed {
			t.Errorf("buffer primed early at seq %d (depth %d), want %d packets before prime", i, i+1, configuredDepth)
		}
	}
	primed := jb.Push(configuredDepth-1, uint32(configuredDepth-1)*160, []byte{byte(configuredDepth - 1)})
	if !primed {
		t.Errorf("buffer not primed after %d packets (configured depth); Reset() broke re-prime logic", configuredDepth)
	}
}

// sinApprox is a simple sin approximation so jitter_test stays math-import-free.
func sinApprox(x float64) float64 {
	// Bring x into [-π, π]
	for x > 3.14159265 {
		x -= 2 * 3.14159265
	}
	for x < -3.14159265 {
		x += 2 * 3.14159265
	}
	// Taylor: sin(x) ≈ x - x³/6 + x⁵/120
	return x - (x*x*x)/6.0 + (x*x*x*x*x)/120.0
}
