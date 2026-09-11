package rtp

import "testing"

// TestAdaptDepthClampsToMaxAdaptDepth exercises the upper-bound clamp in
// adaptDepth(): when the tracked inter-arrival variance (arrivalVarMs) is
// large enough that 3x the variance divided into 10ms frames would exceed
// maxAdaptDepth, the target depth must be clamped down to maxAdaptDepth
// rather than growing unbounded. Prior to this test, that branch (jitter.go
// ~line 273) was never exercised by any existing test: adaptDepth() is only
// ever reached indirectly via 100 Push() calls with small, realistic
// inter-arrival jitter, which never drives arrivalVarMs high enough to hit
// the upper clamp. Directly seeding arrivalVarMs and invoking adaptDepth()
// reaches it deterministically.
func TestAdaptDepthClampsToMaxAdaptDepth(t *testing.T) {
	j := NewJitterBuffer(4)

	// 3 * 1000ms / 10ms = 300 target frames, far above maxAdaptDepth (16).
	j.arrivalVarMs = 1000.0
	j.adaptDepth()

	if j.depth != maxAdaptDepth {
		t.Fatalf("adaptDepth() with huge variance: got depth=%d, want clamped depth=%d", j.depth, maxAdaptDepth)
	}
}

// TestAdaptDepthClampsToMinAdaptDepth exercises the lower-bound clamp: when
// measured jitter is near zero, the computed target depth would round down
// to 0 frames, which must be clamped up to minAdaptDepth so the buffer never
// shrinks below a safe floor.
func TestAdaptDepthClampsToMinAdaptDepth(t *testing.T) {
	j := NewJitterBuffer(4)

	j.arrivalVarMs = 0.0
	j.adaptDepth()

	if j.depth != minAdaptDepth {
		t.Fatalf("adaptDepth() with zero variance: got depth=%d, want clamped depth=%d", j.depth, minAdaptDepth)
	}
}

// TestGetJitterPayloadGrowsBeyondPoolCapacity exercises the branch in
// getJitterPayload() where a slice pulled from jitterPayloadPool has smaller
// capacity than the requested length n, forcing a fresh allocation instead
// of reslicing the pooled buffer. The pool is seeded with 172-byte capacity
// buffers (typical G.711/Opus 20ms payload), so requesting a much larger
// payload exercises the cap(b) < n path that ordinary small-payload tests
// never reach.
func TestGetJitterPayloadGrowsBeyondPoolCapacity(t *testing.T) {
	// Prime the pool with a small buffer, then request more than its capacity.
	small := getJitterPayload(8)
	putJitterPayload(small)

	const want = 4096 // far larger than the pool's 172-byte default capacity
	big := getJitterPayload(want)
	if len(big) != want {
		t.Fatalf("getJitterPayload(%d): got len=%d, want %d", want, len(big), want)
	}
	if cap(big) < want {
		t.Fatalf("getJitterPayload(%d): got cap=%d, want at least %d", want, cap(big), want)
	}
}
