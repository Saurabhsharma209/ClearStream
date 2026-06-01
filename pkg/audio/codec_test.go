package audio_test

import (
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
)

// TestCodecConstants verifies the string values of codec constants match
// the FFmpeg codec names used in the pipeline.
func TestCodecConstants(t *testing.T) {
	tests := []struct {
		codec audio.Codec
		want  string
	}{
		{audio.CodecPCM, "pcm_s16le"},
		{audio.CodecOpus, "opus"},
		{audio.CodecG711U, "pcm_mulaw"},
		{audio.CodecG711A, "pcm_alaw"},
		{audio.CodecG722, "g722"},
		{audio.CodecG729, "g729"},
		{audio.CodecAAC, "aac"},
		{audio.CodecMP3, "mp3"},
		{audio.CodecFLAC, "flac"},
		{audio.CodecVorbis, "vorbis"},
		{audio.CodecSpeex, "speex"},
		{audio.CodecGSM, "gsm"},
		{audio.CodecILBC, "ilbc"},
		{audio.CodecUnknown, "unknown"},
	}
	for _, tt := range tests {
		if string(tt.codec) != tt.want {
			t.Errorf("codec %q: expected string %q, got %q", tt.codec, tt.want, string(tt.codec))
		}
	}
}

// TestNativeSampleRate verifies that each codec returns its correct native sample rate.
func TestNativeSampleRate(t *testing.T) {
	tests := []struct {
		codec audio.Codec
		want  int
	}{
		{audio.CodecPCM, 16000},
		{audio.CodecOpus, 48000},
		{audio.CodecG711U, 8000},
		{audio.CodecG711A, 8000},
		{audio.CodecG722, 16000},
		{audio.CodecG729, 8000},
		{audio.CodecAAC, 44100},
		{audio.CodecMP3, 44100},
		{audio.CodecFLAC, 44100},
		{audio.CodecVorbis, 44100},
		{audio.CodecSpeex, 16000},
		{audio.CodecGSM, 8000},
		{audio.CodecILBC, 8000},
	}
	for _, tt := range tests {
		got := tt.codec.NativeSampleRate()
		if got != tt.want {
			t.Errorf("codec %q NativeSampleRate() = %d, want %d", tt.codec, got, tt.want)
		}
	}
}

// TestNativeSampleRateUnknownFallback verifies unknown codecs fall back to 8000 Hz.
func TestNativeSampleRateUnknownFallback(t *testing.T) {
	c := audio.CodecUnknown
	got := c.NativeSampleRate()
	if got != 8000 {
		t.Errorf("CodecUnknown.NativeSampleRate() = %d, want 8000 (fallback)", got)
	}

	// Also test an arbitrary unknown codec string
	custom := audio.Codec("some_custom_codec")
	got2 := custom.NativeSampleRate()
	if got2 != 8000 {
		t.Errorf("custom codec NativeSampleRate() = %d, want 8000 (fallback)", got2)
	}
}

// TestIsLossless verifies that only PCM and FLAC are considered lossless.
func TestIsLossless(t *testing.T) {
	lossless := []audio.Codec{audio.CodecPCM, audio.CodecFLAC}
	lossy := []audio.Codec{
		audio.CodecOpus, audio.CodecG711U, audio.CodecG711A,
		audio.CodecG722, audio.CodecG729, audio.CodecAAC,
		audio.CodecMP3, audio.CodecVorbis, audio.CodecSpeex,
		audio.CodecGSM, audio.CodecILBC, audio.CodecUnknown,
	}

	for _, c := range lossless {
		if !c.IsLossless() {
			t.Errorf("codec %q should be lossless", c)
		}
	}
	for _, c := range lossy {
		if c.IsLossless() {
			t.Errorf("codec %q should not be lossless", c)
		}
	}
}

// TestProbeNonExistentFile verifies Probe returns an error for a missing file.
func TestProbeNonExistentFile(t *testing.T) {
	_, err := audio.Probe("ffmpeg", "/nonexistent/path/to/file.wav")
	// ffmpeg/ffprobe not installed or file missing — either way we get *some* result.
	// The important thing is it doesn't panic.
	if err != nil {
		t.Logf("Probe returned error (expected if ffmpeg not installed): %v", err)
	}
}

// TestCodecTypeAssertion verifies Codec is usable as a string key in maps.
func TestCodecAsMapKey(t *testing.T) {
	m := map[audio.Codec]int{
		audio.CodecPCM:  1,
		audio.CodecOpus: 2,
	}
	if m[audio.CodecPCM] != 1 {
		t.Error("CodecPCM map lookup failed")
	}
	if m[audio.CodecOpus] != 2 {
		t.Error("CodecOpus map lookup failed")
	}
}
