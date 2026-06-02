package audio

import "testing"

func TestBandMode_SampleRate(t *testing.T) {
	cases := []struct {
		mode BandMode
		want int
	}{
		{BandNarrow, 8000},
		{BandWide, 16000},
		{BandSuperWide, 32000},
		{BandFull, 48000},
	}
	for _, c := range cases {
		if got := c.mode.SampleRate(); got != c.want {
			t.Errorf("BandMode(%d).SampleRate() = %d, want %d", c.mode, got, c.want)
		}
	}
}

func TestBandFromRTPPayloadType(t *testing.T) {
	cases := []struct {
		pt   uint8
		want BandMode
	}{
		{0, BandNarrow},   // PCMU G.711 µ-law
		{8, BandNarrow},   // PCMA G.711 A-law
		{9, BandWide},     // G.722 — wideband despite RFC 3551 clock quirk
		{111, BandFull},   // Opus fullband (WebRTC/FreeSWITCH common PT)
		{255, BandNarrow}, // unknown → conservative narrowband default
	}
	for _, c := range cases {
		if got := BandFromRTPPayloadType(c.pt); got != c.want {
			t.Errorf("BandFromRTPPayloadType(%d) = %v, want %v", c.pt, got, c.want)
		}
	}
}

func TestBandFromSampleRate(t *testing.T) {
	cases := []struct {
		hz   int
		want BandMode
	}{
		{8000, BandNarrow},
		{16000, BandWide},
		{32000, BandSuperWide},
		{48000, BandFull},
		{44100, BandFull},  // CD audio → fullband
		{4000, BandNarrow}, // below 8kHz → narrowband
	}
	for _, c := range cases {
		if got := BandFromSampleRate(c.hz); got != c.want {
			t.Errorf("BandFromSampleRate(%d) = %v, want %v", c.hz, got, c.want)
		}
	}
}

func TestProcessorSampleRate(t *testing.T) {
	if ProcessorSampleRate != 16000 {
		t.Errorf("ProcessorSampleRate = %d, want 16000", ProcessorSampleRate)
	}
}

func TestNeedsUpsample(t *testing.T) {
	if !NeedsUpsample(8000) {
		t.Error("NeedsUpsample(8000) should be true")
	}
	if NeedsUpsample(16000) {
		t.Error("NeedsUpsample(16000) should be false")
	}
	if NeedsUpsample(48000) {
		t.Error("NeedsUpsample(48000) should be false")
	}
}

func TestNeedsDownsample(t *testing.T) {
	if NeedsDownsample(8000) {
		t.Error("NeedsDownsample(8000) should be false")
	}
	if NeedsDownsample(16000) {
		t.Error("NeedsDownsample(16000) should be false")
	}
	if !NeedsDownsample(48000) {
		t.Error("NeedsDownsample(48000) should be true")
	}
}
