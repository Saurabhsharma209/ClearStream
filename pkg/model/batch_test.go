package model_test

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

func TestBatchWrapper_MatchesSequential(t *testing.T) {
	s := model.NewPassthrough()
	bs := model.AsBatch(s)

	frames := make([][]int16, 5)
	for i := range frames {
		f := make([]int16, 160)
		for j := range f {
			f[j] = int16((i + 1) * (j + 1) % 32767)
		}
		frames[i] = f
	}

	// Sequential
	seqOut := make([][]int16, 5)
	s2 := model.NewPassthrough()
	for i, f := range frames {
		out, err := s2.Process(f)
		if err != nil {
			t.Fatal(err)
		}
		seqOut[i] = out
	}

	// Batch
	batchOut, err := bs.ProcessBatch(frames)
	if err != nil {
		t.Fatal(err)
	}
	if len(batchOut) != len(seqOut) {
		t.Fatalf("length mismatch: got %d want %d", len(batchOut), len(seqOut))
	}
	for i := range seqOut {
		if len(batchOut[i]) != len(seqOut[i]) {
			t.Fatalf("frame %d length mismatch", i)
		}
	}
}

func TestAsBatch_AlreadyBatchSuppressor(t *testing.T) {
	s := model.NewPassthrough()
	bs1 := model.AsBatch(s)
	bs2 := model.AsBatch(bs1) // should not double-wrap
	if bs1 != bs2 {
		t.Fatal("AsBatch should return the same BatchSuppressor if already wrapped")
	}
}

func BenchmarkBatchVsSequential(b *testing.B) {
	frames := make([][]int16, 32)
	for i := range frames {
		frames[i] = make([]int16, 160)
	}
	s := model.NewPassthrough()
	bs := model.AsBatch(s)

	b.Run("sequential", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			for _, f := range frames {
				s.Process(f)
			}
		}
	})
	b.Run("batch", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			bs.ProcessBatch(frames)
		}
	})
}
