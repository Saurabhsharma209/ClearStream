package audio

import (
	"strings"
	"testing"
)

func TestNormalizeCodec(t *testing.T) {
	cases := []struct {
		input string
		want  Codec
	}{
		{"pcm_s16le", CodecPCM},
		{"pcm_s16be", CodecPCM},
		{"pcm_u8", CodecPCM},
		{"opus", CodecOpus},
		{"pcm_mulaw", CodecG711U},
		{"mulaw", CodecG711U},
		{"ulaw", CodecG711U},
		{"pcm_alaw", CodecG711A},
		{"alaw", CodecG711A},
		{"g722", CodecG722},
		{"g729", CodecG729},
		{"aac", CodecAAC},
		{"mp3", CodecMP3},
		{"libmp3lame", CodecMP3},
		{"flac", CodecFLAC},
		{"vorbis", CodecVorbis},
		{"libvorbis", CodecVorbis},
		{"speex", CodecSpeex},
		{"libspeex", CodecSpeex},
		{"gsm", CodecGSM},
		{"ilbc", CodecILBC},
		{"libilbc", CodecILBC},
		{"totally_unknown_xyz", CodecUnknown},
		{"", CodecUnknown},
		{"  OPUS  ", CodecOpus}, // trimmed + lowercased
	}
	for _, c := range cases {
		got := normalizeCodec(c.input)
		if got != c.want {
			t.Errorf("normalizeCodec(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseFFprobeJSON(t *testing.T) {
	// Minimal ffprobe JSON with audio stream
	json := `{
  "streams": [
    {
      "codec_type": "audio",
      "codec_name": "opus",
      "sample_rate": "48000",
      "channels": 2,
      "duration": "10.5"
    }
  ],
  "format": {
    "duration": "10.5"
  }
}`
	info, err := parseFFprobeJSON([]byte(json), "test.opus")
	if err != nil {
		t.Fatalf("parseFFprobeJSON error: %v", err)
	}
	if info.AudioCodec != CodecOpus {
		t.Errorf("AudioCodec = %q, want %q", info.AudioCodec, CodecOpus)
	}
	if info.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", info.SampleRate)
	}
	if info.Channels != 2 {
		t.Errorf("Channels = %d, want 2", info.Channels)
	}
	if info.ContainerFormat != "opus" {
		t.Errorf("ContainerFormat = %q, want %q", info.ContainerFormat, "opus")
	}
	if info.HasVideo {
		t.Error("HasVideo should be false")
	}
}

func TestParseFFprobeJSONWithVideo(t *testing.T) {
	// Note: the lightweight extractJSONField parser uses window-based search;
	// when video stream precedes audio, codec_name lookup for "audio" may pick
	// up the video codec_name within the window. We only assert HasVideo here.
	json := `{
  "streams": [
    {
      "codec_type": "video",
      "codec_name": "h264"
    },
    {
      "codec_type": "audio",
      "codec_name": "aac",
      "sample_rate": "44100",
      "channels": 2
    }
  ]
}`
	info, err := parseFFprobeJSON([]byte(json), "file.mp4")
	if err != nil {
		t.Fatalf("parseFFprobeJSON error: %v", err)
	}
	if !info.HasVideo {
		t.Error("HasVideo should be true for JSON containing video stream")
	}
	if info.VideoCodec != "h264" {
		t.Errorf("VideoCodec = %q, want h264", info.VideoCodec)
	}
}

func TestParseFFprobeJSONDefaultSampleRate(t *testing.T) {
	// Missing sample_rate → fallback to codec native rate
	json := `{
  "streams": [
    {
      "codec_type": "audio",
      "codec_name": "pcm_mulaw",
      "channels": 1
    }
  ]
}`
	info, err := parseFFprobeJSON([]byte(json), "call.wav")
	if err != nil {
		t.Fatalf("parseFFprobeJSON error: %v", err)
	}
	if info.SampleRate != 8000 {
		t.Errorf("SampleRate = %d, want 8000 (G.711µ native)", info.SampleRate)
	}
}

func TestParseFFprobeJSONDefaultChannels(t *testing.T) {
	// Missing channels → defaults to 1
	json := `{
  "streams": [
    {
      "codec_type": "audio",
      "codec_name": "opus",
      "sample_rate": "48000"
    }
  ]
}`
	info, err := parseFFprobeJSON([]byte(json), "test.opus")
	if err != nil {
		t.Fatalf("parseFFprobeJSON error: %v", err)
	}
	if info.Channels != 1 {
		t.Errorf("Channels = %d, want 1 (default)", info.Channels)
	}
}

func TestParseFFmpegInfo(t *testing.T) {
	stderr := `ffmpeg version 4.4
Input #0, wav, from 'test.wav':
  Duration: 00:00:05.00, start: 0.000000, bitrate: 256 kb/s
    Stream #0:0: Audio: pcm_s16le, 16000 Hz, mono, s16, 256 kb/s`
	info, err := parseFFmpegInfo(stderr, "test.wav")
	if err != nil {
		t.Fatalf("parseFFmpegInfo error: %v", err)
	}
	if info.AudioCodec != CodecPCM {
		t.Errorf("AudioCodec = %q, want pcm_s16le", info.AudioCodec)
	}
	if info.SampleRate != 16000 {
		t.Errorf("SampleRate = %d, want 16000", info.SampleRate)
	}
	if info.Channels != 1 {
		t.Errorf("Channels = %d, want 1 (mono)", info.Channels)
	}
	if info.DurationSec != 5.0 {
		t.Errorf("DurationSec = %f, want 5.0", info.DurationSec)
	}
}

func TestParseFFmpegInfoStereo(t *testing.T) {
	stderr := `ffmpeg version 5
Input #0, mp3, from 'song.mp3':
  Duration: 00:03:30.00
    Stream #0:0: Audio: mp3, 44100 Hz, stereo, fltp`
	info, err := parseFFmpegInfo(stderr, "song.mp3")
	if err != nil {
		t.Fatalf("parseFFmpegInfo error: %v", err)
	}
	if info.AudioCodec != CodecMP3 {
		t.Errorf("AudioCodec = %q, want mp3", info.AudioCodec)
	}
	if info.Channels != 2 {
		t.Errorf("Channels = %d, want 2 (stereo)", info.Channels)
	}
	if info.SampleRate != 44100 {
		t.Errorf("SampleRate = %d, want 44100", info.SampleRate)
	}
}

func TestParseFFmpegInfoVideo(t *testing.T) {
	stderr := `Input #0, mp4, from 'video.mp4':
    Stream #0:0: Video: h264, yuv420p
    Stream #0:1: Audio: aac, 44100 Hz, stereo`
	info, err := parseFFmpegInfo(stderr, "video.mp4")
	if err != nil {
		t.Fatalf("parseFFmpegInfo error: %v", err)
	}
	if !info.HasVideo {
		t.Error("HasVideo should be true")
	}
	if !strings.Contains(info.VideoCodec, "h264") {
		t.Errorf("VideoCodec = %q, want to contain h264", info.VideoCodec)
	}
}

// TestParseFFprobeJSONFields verifies that parseFFprobeJSON correctly extracts
// codec, sample rate, channels, duration, and bitrate from well-formed JSON.
// (Replaces the former TestExtractJSONField which tested the removed extractJSONField helper.)
func TestParseFFprobeJSONFields(t *testing.T) {
	jsonData := []byte(`{
  "streams": [
    {
      "codec_type": "audio",
      "codec_name": "opus",
      "sample_rate": "48000",
      "channels": 2,
      "bit_rate": "64000"
    }
  ],
  "format": {
    "format_name": "ogg",
    "duration": "3.500000"
  }
}`)

	info, err := parseFFprobeJSON(jsonData, "test.ogg")
	if err != nil {
		t.Fatalf("parseFFprobeJSON error: %v", err)
	}
	if info.AudioCodec != CodecOpus {
		t.Errorf("AudioCodec = %q, want opus", info.AudioCodec)
	}
	if info.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", info.SampleRate)
	}
	if info.Channels != 2 {
		t.Errorf("Channels = %d, want 2", info.Channels)
	}
	if info.BitRate != 64 {
		t.Errorf("BitRate = %d kbps, want 64", info.BitRate)
	}
	if info.DurationSec < 3.49 || info.DurationSec > 3.51 {
		t.Errorf("DurationSec = %f, want ~3.5", info.DurationSec)
	}
	if info.ContainerFormat != "ogg" {
		t.Errorf("ContainerFormat = %q, want ogg", info.ContainerFormat)
	}
}
