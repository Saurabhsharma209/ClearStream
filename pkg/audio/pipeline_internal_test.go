package audio

import (
	"testing"
)

func TestPipelineByteOrderRoundtrip(t *testing.T) {
	values := []int16{-32768, -1, 0, 1, 32767}
	b := int16ToBytes(values)
	got := bytesToInt16(b)
	if len(got) != len(values) {
		t.Fatalf("length mismatch: want %d, got %d", len(values), len(got))
	}
	for i, want := range values {
		if got[i] != want {
			t.Errorf("sample[%d]: want %d, got %d", i, want, got[i])
		}
	}
}
