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

// TestUlawRoundtripAll256 verifies that for every µ-law codeword the
// decode→encode→decode sequence is idempotent (G.711 correctness property).
func TestUlawRoundtripAll256(t *testing.T) {
	for i := 0; i < 256; i++ {
		codeword := byte(i)
		pcm1 := ulawToLinear(codeword)
		reencoded := linearToUlaw(pcm1)
		pcm2 := ulawToLinear(reencoded)
		if pcm1 != pcm2 {
			t.Errorf("ulaw codeword 0x%02X: decode→encode→decode not idempotent: first=%d second=%d", codeword, pcm1, pcm2)
		}
	}
}

// TestAlawRoundtripAll256 verifies that for every A-law codeword the
// decode→encode→decode sequence is idempotent (G.711 correctness property).
func TestAlawRoundtripAll256(t *testing.T) {
	for i := 0; i < 256; i++ {
		codeword := byte(i)
		pcm1 := alawToLinear(codeword)
		reencoded := linearToAlaw(pcm1)
		pcm2 := alawToLinear(reencoded)
		if pcm1 != pcm2 {
			t.Errorf("alaw codeword 0x%02X: decode→encode→decode not idempotent: first=%d second=%d", codeword, pcm1, pcm2)
		}
	}
}

// TestUlawSilence verifies that encoding PCM silence (0) and decoding the
// result yields a value near zero (within µ-law quantisation noise floor).
func TestUlawSilence(t *testing.T) {
	encoded := linearToUlaw(0)
	decoded := ulawToLinear(encoded)
	if decoded < -200 || decoded > 200 {
		t.Errorf("ulaw silence: encode(0)=0x%02X, decode back=%d, want |pcm|<200", encoded, decoded)
	}
}

// TestUlawSymmetry verifies that positive and negative PCM values encode to
// different µ-law bytes and decode back to values of opposite sign.
func TestUlawSymmetry(t *testing.T) {
	cases := []int16{1000, 2000, 4000, 8000}
	for _, v := range cases {
		posCode := linearToUlaw(v)
		negCode := linearToUlaw(-v)
		if posCode == negCode {
			t.Errorf("ulaw symmetry PCM ±%d: positive and negative encode to same codeword 0x%02X", v, posCode)
		}
		posPCM := ulawToLinear(posCode)
		negPCM := ulawToLinear(negCode)
		if posPCM <= 0 {
			t.Errorf("ulaw symmetry PCM +%d: decoded value should be positive, got %d", v, posPCM)
		}
		if negPCM >= 0 {
			t.Errorf("ulaw symmetry PCM -%d: decoded value should be negative, got %d", v, negPCM)
		}
	}
}
