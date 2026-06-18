// Package file -- internal whitebox tests for unexported helpers.
// Uses package file (not file_test) to access unexported symbols.
package file

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"go.uber.org/zap"
)

// ___ inferOutputCodec _________________________________________________________

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
		{"out.unknown", "aac"},
		{"out", "aac"},
	}
	for _, tc := range cases {
		got := inferOutputCodec(tc.dst)
		if got != tc.want {
			t.Errorf("inferOutputCodec(%q) = %q, want %q", tc.dst, got, tc.want)
		}
	}
}

// ___ parseFFmpegTime __________________________________________________________

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
		{"00:00:00", 0},
	}
	for _, tc := range cases {
		got := parseFFmpegTime(tc.input)
		if math.Abs(got-tc.want) > 0.001 {
			t.Errorf("parseFFmpegTime(%q) = %.4f, want %.4f", tc.input, got, tc.want)
		}
	}
}

// ___ parseFFmpegError _________________________________________________________

func TestParseFFmpegError(t *testing.T) {
	cases := []struct {
		name    string
		stderr  string
		wantErr error
	}{
		{"no such file", "ffmpeg: No such file or directory", ErrFileNotFound},
		{"permission denied", "error: permission denied opening /tmp/x", ErrPermission},
		{"unknown encoder", "Unknown encoder libfoo", ErrCodecNotFound},
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

// ___ ProcessDir/ProcessDirFull subdirectory + unsupported-file branches ______

// TestProcessDirSkipsSubdirectories covers the e.IsDir() continue branch.
func TestProcessDirSkipsSubdirectories(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.MkdirAll(src+"/subdir", 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Logger:     zap.NewNop(),
	})

	errs := p.ProcessDir(src, dst, Options{})
	if len(errs) != 0 {
		t.Errorf("expected nil errors for dir with only subdirs, got %v", errs)
	}
}

// TestProcessDirFullSkipsSubdirectories covers the e.IsDir() continue branch in ProcessDirFull.
func TestProcessDirFullSkipsSubdirectories(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.MkdirAll(src+"/nested", 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Logger:     zap.NewNop(),
	})

	results := p.ProcessDirFull(src, dst, Options{})
	if len(results) != 0 {
		t.Errorf("expected empty results for dir with only subdirs, got %d", len(results))
	}
}

// TestProcessDirSkipsUnsupportedFiles covers the unsupported-extension branch in ProcessDir.
func TestProcessDirSkipsUnsupportedFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	for _, name := range []string{"notes.txt", "data.json", "image.png"} {
		if err := os.WriteFile(src+"/"+name, []byte("dummy"), 0644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Logger:     zap.NewNop(),
	})

	errs := p.ProcessDir(src, dst, Options{})
	if len(errs) != 0 {
		t.Errorf("expected nil errs for unsupported files, got %v", errs)
	}
}

// ___ StreamProcess error paths ________________________________________________

// failingSuppressor is a Suppressor whose Process always returns an error.
type failingSuppressor struct{}

func (f *failingSuppressor) Process(_ []int16) ([]int16, error) {
	return nil, errors.New("suppressor: simulated failure")
}
func (f *failingSuppressor) Reset()       {}
func (f *failingSuppressor) Close() error { return nil }
func (f *failingSuppressor) Name() string { return "failing" }

// TestStreamProcessFlushError: non-frame-aligned input causes Flush to call
// Suppressor.Process on leftover bytes; failingSuppressor returns an error.
func TestStreamProcessFlushError(t *testing.T) {
	partialInput := make([]byte, audio.FrameSizeBytes-1)
	r := bytes.NewReader(partialInput)
	var w bytes.Buffer

	opts := Options{Suppressor: &failingSuppressor{}}

	err := StreamProcess(context.Background(), r, &w, opts)
	if err == nil {
		t.Error("expected StreamProcess to return error when Flush fails, got nil")
	}
}

// TestStreamProcessSuppressFrameError: exactly one full frame causes ProcessFrames
// to call Suppressor.Process which fails.
func TestStreamProcessSuppressFrameError(t *testing.T) {
	oneFrame := make([]byte, audio.FrameSizeBytes)
	r := bytes.NewReader(oneFrame)
	var w bytes.Buffer

	opts := Options{Suppressor: &failingSuppressor{}}

	err := StreamProcess(context.Background(), r, &w, opts)
	if err == nil {
		t.Error("expected StreamProcess to return error when frame suppression fails, got nil")
	}
}

// ___ ProcessDir / ProcessDirFull MkdirAll failure branch ____________________

// TestProcessDirMkdirAllFails covers the os.MkdirAll error branch by using
// an existing regular file as the destination directory path.
func TestProcessDirMkdirAllFails(t *testing.T) {
	src := t.TempDir()

	// Create a regular file at the dst path so MkdirAll fails.
	f, err := os.CreateTemp("", "clearstream-dstfile-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()
	dstFile := f.Name()
	defer os.Remove(dstFile)

	// Write a supported audio file in src so at least one job would be queued
	// and we actually reach the MkdirAll call.
	if err := os.WriteFile(src+"/test.wav", []byte("dummy"), 0644); err != nil {
		t.Fatalf("create dummy wav: %v", err)
	}

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Logger:     zap.NewNop(),
	})

	// dst is a file, not a directory -> MkdirAll should fail.
	errs := p.ProcessDir(src, dstFile+"/subpath", Options{})
	if len(errs) == 0 {
		t.Error("expected error when dst path is invalid, got none")
	}
}

// TestProcessDirFullMkdirAllFails covers the os.MkdirAll error branch in ProcessDirFull.
func TestProcessDirFullMkdirAllFails(t *testing.T) {
	src := t.TempDir()

	f, err := os.CreateTemp("", "clearstream-dstfile-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()
	dstFile := f.Name()
	defer os.Remove(dstFile)

	if err := os.WriteFile(src+"/test.mp3", []byte("dummy"), 0644); err != nil {
		t.Fatalf("create dummy mp3: %v", err)
	}

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Logger:     zap.NewNop(),
	})

	results := p.ProcessDirFull(src, dstFile+"/subpath", Options{})
	if len(results) == 0 {
		t.Error("expected error result when dst path is invalid, got none")
	}
	if results[0].Err == nil {
		t.Error("expected error in result[0].Err, got nil")
	}
}

// ___ ProcessWithOptions early-path branches __________________________________

// TestProcessWithOptionsOnProgressBeforeProbe verifies that OnProgress(0.0)
// is called before audio.Probe even when ffmpeg is not available.
// This covers the first OnProgress branch in ProcessWithOptions.
func TestProcessWithOptionsOnProgressBeforeProbe(t *testing.T) {
	var calls []float64

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "/nonexistent/ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Logger:     zap.NewNop(),
	})

	opts := Options{
		OnProgress: func(pct float64) {
			calls = append(calls, pct)
		},
	}

	// This will fail at the probe step, but OnProgress(0.0) fires first.
	_ = p.ProcessWithOptions("/tmp/clearstream_nope.wav", "/tmp/clearstream_out.wav", opts)

	if len(calls) == 0 {
		t.Fatal("expected OnProgress to be called at least once before probe failure")
	}
	if calls[0] != 0.0 {
		t.Errorf("expected first OnProgress call to be 0.0, got %f", calls[0])
	}
}
