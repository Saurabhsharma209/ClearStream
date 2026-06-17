// Package file — internal whitebox tests for unexported helpers.
// Uses `package file` (not file_test) to access unexported symbols.
package file

import (
	"math"
	"testing"
)

// ─── inferOutputCodec ─────────────────────────────────────────────────────────

func TestInferOutputCodec(t *testing.T) {
	cases := []struct {
		dst  string
		want string
	}{
		{"out.mp3", "mp3"},
		{"out.MP3", "mp3"},
		{"out.opus", "opus"},
		{"out.ogg", "opus"},
		{"out.flac", "flac"},
		{"out.wav", "pcm_s16le"},
		{"out.aac", "aac"},
		{"out.m4a", "aac"},
		{"out.mp4", "aac"},
		{"out.mov", "aac"},
		{"out.mkv", "aac"},
		{"out.webm", "aac"},
		{"out.unknown", "aac"}, // default fallback
		{"out", "aac"},         // no extension → default
	}
	for _, tc := range cases {
		got := inferOutputCodec(tc.dst)
		if got != tc.want {
			t.Errorf("inferOutputCodec(%q) = %q, want %q", tc.dst, got, tc.want)
		}
	}
}

// ─── parseFFmpegTime ──────────────────────────────────────────────────────────

func TestParseFFmpegTime(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{"00:00:00.00", 0},
		{"00:00:01.00", 1.0},
		{"00:01:00.00", 60.0},
		{"01:00:00.00", 3600.0},
		{"00:01:30.50", 90.5},
		{"01:02:03.75", 3723.75},
		{"notatime", 0},
		{"", 0},
		{"00:00:00", 0}, // only 2 fields parsed → n<3 → 0
	}
	for _, tc := range cases {
		got := parseFFmpegTime(tc.input)
		if math.Abs(got-tc.want) > 0.001 {
			t.Errorf("parseFFmpegTime(%q) = %.4f, want %.4f", tc.input, got, tc.want)
		}
	}
}

// ─── parseFFmpegError ─────────────────────────────────────────────────────────

func TestParseFFmpegError(t *testing.T) {
	cases := []struct {
		name    string
		stderr  string
		wantErr error
	}{
		{"no such file", "ffmpeg: No such file or directory", ErrFileNotFound},
		{"permission denied", "error: permission denied opening /tmp/x", ErrPermission},
		{"unknown encoder", "Unknown encoder 'libfoo'", ErrCodecNotFound},
		{"encoder not found", "Encoder not found for codec mp3", ErrCodecNotFound},
		{"decoder not found", "Decoder not found: pcm_bogus", ErrCodecNotFound},
		{"unrelated error", "some random ffmpeg crash", nil},
		{"empty stderr", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFFmpegError(tc.stderr)
			if got != tc.wantErr {
				t.Errorf("parseFFmpegError(%q) = %v, want %v", tc.stderr, got, tc.wantErr)
			}
		})
	}
}
