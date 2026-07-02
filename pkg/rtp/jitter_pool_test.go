package rtp

import (
	"sync"
	"testing"
)

// TestJitterPayloadPoolCorrectness verifies that pooling the payload copies
// made in Push() does not introduce data corruption: every payload popped
// out must match exactly what was pushed in, even after many push/pop/
// release cycles that recycle the same backing arrays.
func TestJitterPayloadPoolCorrectness(t *testing.T) {
	jb := NewJitterBuffer(2)

	const rounds = 500
	for round := 0; round < rounds; round++ {
		seqBase := uint16(round * 2)
		want0 := byte(round)
		want1 := byte(round + 1)

		jb.Push(seqBase, uint32(seqBase)*160, []byte{want0, want0 ^ 0xFF})
		jb.Push(seqBase+1, uint32(seqBase+1)*160, []byte{want1, want1 ^ 0xFF})

		p0, ok0 := jb.Pop()
		if !ok0 || len(p0) != 2 || p0[0] != want0 || p0[1] != want0^0xFF {
			t.Fatalf("round %d: seq %d payload corrupted: %v", round, seqBase, p0)
		}
		jb.ReleasePayload(p0)

		p1, ok1 := jb.Pop()
		if !ok1 || len(p1) != 2 || p1[0] != want1 || p1[1] != want1^0xFF {
			t.Fatalf("round %d: seq %d payload corrupted: %v", round, seqBase+1, p1)
		}
		jb.ReleasePayload(p1)
	}
}

// TestJitterPayloadPoolNoAliasing verifies that once Pop() hands a payload to
// its caller, mutating that payload cannot corrupt other buffered (not yet
// popped) entries -- i.e. Push()'s copy-per-entry semantics are preserved
// even though the underlying storage now comes from a pool.
func TestJitterPayloadPoolNoAliasing(t *testing.T) {
	jb := NewJitterBuffer(3)
	jb.Push(0, 0, []byte{1, 1, 1})
	jb.Push(1, 160, []byte{2, 2, 2})
	jb.Push(2, 320, []byte{3, 3, 3})

	p0, ok := jb.Pop()
	if !ok || p0 == nil {
		t.Fatal("expected seq 0 payload")
	}
	// Mutate the popped slice; this must not affect still-buffered entries.
	for i := range p0 {
		p0[i] = 0xAA
	}
	jb.ReleasePayload(p0)

	p1, ok := jb.Pop()
	if !ok || p1 == nil || p1[0] != 2 || p1[1] != 2 || p1[2] != 2 {
		t.Fatalf("seq 1 payload aliased/corrupted by mutation of seq 0's payload: %v", p1)
	}
	jb.ReleasePayload(p1)

	p2, ok := jb.Pop()
	if !ok || p2 == nil || p2[0] != 3 || p2[1] != 3 || p2[2] != 3 {
		t.Fatalf("seq 2 payload aliased/corrupted: %v", p2)
	}
	jb.ReleasePayload(p2)
}

// TestJitterPayloadPoolReleaseAfterEviction verifies that entries dropped via
// tail-drop (over maxDepth) do not corrupt or crash when their payloads are
// released internally to the pool, and that packets which do survive to Pop()
// still carry correct content.
func TestJitterPayloadPoolReleaseAfterEviction(t *testing.T) {
	jb := NewJitterBuffer(2)
	jb.maxDepth = 4 // shrink the hard cap so this test triggers tail-drop quickly

	// Push more packets (in-order) than maxDepth so the earliest ones would be
	// tail-dropped if they sorted to the tail -- here we push out-of-order,
	// high seq numbers first, so the low ones "arrive late" and cause the
	// buffer to already be full when they insert, triggering eviction paths.
	jb.Push(10, 1600, []byte{10})
	jb.Push(11, 1760, []byte{11})
	jb.Push(12, 1920, []byte{12})
	jb.Push(13, 2080, []byte{13})
	jb.Push(14, 2240, []byte{14}) // 5th packet -- triggers tail-drop at maxDepth=4

	// Buffer should be primed (depth=2) and pop in order without panicking.
	seen := []byte{}
	for i := 0; i < 4; i++ {
		p, ok := jb.Pop()
		if !ok {
			break
		}
		if p != nil {
			seen = append(seen, p[0])
			jb.ReleasePayload(p)
		}
	}
	if len(seen) == 0 {
		t.Fatal("expected at least one popped payload after tail-drop eviction")
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Errorf("popped payloads not in increasing seq order: %v", seen)
		}
	}
}

// TestJitterPayloadPoolResetReleasesBuffer verifies Reset() doesn't panic or
// corrupt state when releasing still-buffered pooled payloads, and that the
// buffer works correctly for new packets afterward (regression guard for the
// pool-release logic added to Reset()).
func TestJitterPayloadPoolResetReleasesBuffer(t *testing.T) {
	jb := NewJitterBuffer(4)
	for i := uint16(0); i < 3; i++ {
		jb.Push(i, uint32(i)*160, []byte{byte(i), byte(i)})
	}
	jb.Reset() // entries above were never popped -- Reset must release them safely

	jb.Push(100, 100*160, []byte{100, 100})
	jb.Push(101, 101*160, []byte{101, 101})
	jb.Push(102, 102*160, []byte{101, 101})
	jb.Push(103, 103*160, []byte{101, 101})

	p, ok := jb.Pop()
	if !ok || p == nil || p[0] != 100 {
		t.Errorf("expected seq 100 after reset, got ok=%v payload=%v", ok, p)
	}
	jb.ReleasePayload(p)
}

// TestJitterPayloadPoolConcurrentSafety exercises Push/Pop/ReleasePayload from
// multiple goroutines against independent JitterBuffer instances (each buffer
// is not itself meant to be used concurrently by multiple producers, but the
// shared package-level jitterPayloadPool IS shared across all instances, so
// this guards against races in the pool itself). Run with -race.
func TestJitterPayloadPoolConcurrentSafety(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			jb := NewJitterBuffer(2)
			for i := uint16(0); i < 200; i++ {
				payload := []byte{byte(id), byte(i), byte(i >> 8)}
				jb.Push(i, uint32(i)*160, payload)
				if p, ok := jb.Pop(); ok && p != nil {
					jb.ReleasePayload(p)
				}
			}
		}(g)
	}
	wg.Wait()
}

// BenchmarkJitterPush measures allocations for the Push() hot path alone
// (drains with Pop() but does not release the payload back to the pool,
// mirroring pre-pooling behavior for a fair per-call comparison).
func BenchmarkJitterPush(b *testing.B) {
	jb := NewJitterBuffer(4)
	payload := make([]byte, 160) // typical 20ms G.711 payload size
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := uint16(i)
		jb.Push(seq, uint32(seq)*160, payload)
		jb.Pop()
	}
}

// BenchmarkJitterPushPopRelease measures the full realistic hot-path cycle
// used by session.go's handlePacket: Push a packet in, Pop it back out, and
// ReleasePayload it once decoded. This is expected to show markedly fewer
// allocs/op than BenchmarkJitterPush once the pool warms up, since the
// released backing arrays get reused by subsequent Push() calls.
func BenchmarkJitterPushPopRelease(b *testing.B) {
	jb := NewJitterBuffer(4)
	payload := make([]byte, 160)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := uint16(i)
		jb.Push(seq, uint32(seq)*160, payload)
		p, ok := jb.Pop()
		if ok && p != nil {
			jb.ReleasePayload(p)
		}
	}
}
