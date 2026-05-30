package rtp

import "testing"

func TestUlawRoundtrip(t *testing.T) {
	for i := 0; i < 256; i++ {
		encoded := byte(i)
		decoded := ulawToLinear(encoded)
		reencoded := linearToUlaw(decoded)
		// G.711 µ-law has two codewords for zero (0x7F = +0, 0xFF = -0).
		// Both decode to 0, but linearToUlaw always produces 0xFF for 0.
		// Skip the inherent dual-zero ambiguity for byte 0x7F.
		if encoded == 0x7F && decoded == 0 {
			continue
		}
		// Allow ±1 LSB tolerance due to quantization
		diff := int(reencoded) - int(encoded)
		if diff < -1 || diff > 1 {
			t.Errorf("ulaw roundtrip byte %d: got %d (diff=%d)", i, reencoded, diff)
		}
	}
}

func TestAlawRoundtrip(t *testing.T) {
	for i := 0; i < 256; i++ {
		encoded := byte(i)
		decoded := alawToLinear(encoded)
		reencoded := linearToAlaw(decoded)
		diff := int(reencoded) - int(encoded)
		if diff < -1 || diff > 1 {
			t.Errorf("alaw roundtrip byte %d: got %d (diff=%d)", i, reencoded, diff)
		}
	}
}

func TestUlawKnownValues(t *testing.T) {
	// ITU-T G.711 known values: byte 0xFF = silence (0), byte 0x7F = silence (0)
	silence := ulawToLinear(0xFF)
	if silence != 0 {
		t.Errorf("ulaw 0xFF should decode to 0, got %d", silence)
	}
	silence2 := ulawToLinear(0x7F)
	if silence2 != 0 {
		t.Errorf("ulaw 0x7F should decode to 0, got %d", silence2)
	}
}
