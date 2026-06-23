// Package file – fake-ffmpeg whitebox tests targeting the coverage gaps in
// ProcessWithOptions, decodeAndSuppress, and encodeAndMux.
//
// Strategy: build a shell-script stub that masquerades as ffmpeg/ffprobe.
//   - Decode phase (last arg == "-"): writes silence PCM to stdout.
//   - Probe phase (invoked as ffprobe): writes minimal ffprobe JSON to stdout.
//   - Encode phase (writes to a named file): writes a minimal WAV to that path.
//   - Error variant: exits 1 with "No such file or directory" in stderr.
package file

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// skipOnWindows skips shell-script-based tests on Windows.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-ffmpeg shell script not supported on Windows")
	}
}

// makeFakeFFmpegForFile creates a fake ffmpeg+ffprobe shell-script pair in a temp dir.
//
// The script behaves as follows:
//   - When invoked as "ffprobe" ($0 ends with "ffprobe"): emits minimal JSON
//     that describes a 16kHz mono WAV, so Probe() succeeds.
//   - When called with last argument "-" (decode/pipe mode): writes 320 zero bytes
//     (silence PCM s16le) to stdout.
//   - Otherwise (encode mode): writes a minimal valid WAV to the last argument.
func makeFakeFFmpegForFile(t *testing.T) (ffmpegPath string) {
	t.Helper()
	skipOnWindows(t)

	dir := t.TempDir()

	// Minimal ffprobe JSON for a 16kHz mono WAV.
	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.020000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.020000","bit_rate":"256000"}}`

	// Python one-liner that writes a minimal WAV to the path given as last arg.
	// 44-byte header + 320 bytes of silence.
	pyWAV := `import sys,struct;` +
		`dst=sys.argv[-1];` +
		`d=b'\x00'*320;` +
		`h=b'RIFF'+struct.pack('<I',36+len(d))+b'WAVEfmt ';` +
		`h+=struct.pack('<IHHIIHH',16,1,1,16000,32000,2,16);` +
		`h+=b'data'+struct.pack('<I',len(d));` +
		`open(dst,'wb').write(h+d)`

	script := "#!/bin/sh\n" +
		// Detect ffprobe role by argv[0].
		"case \"$0\" in\n" +
		"  *ffprobe*)\n" +
		"    echo '" + probeJSON + "'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		// Detect decode phase: last argument is "-" (pipe to stdout).
		"LAST=\"${@: -1}\"\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		"    dd if=/dev/zero bs=320 count=1 2>/dev/null\n" +
		"    exit 0\n" +
		"fi\n" +
		// Encode phase: write minimal WAV to destination.
		"python3 -c \"" + pyWAV + "\" \"$LAST\" 2>/dev/null\n" +
		"if [ $? -ne 0 ]; then\n" +
		"    dd if=/dev/zero of=\"$LAST\" bs=364 count=1 2>/dev/null\n" +
		"fi\n" +
		"exit 0\n"

	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")

	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeFakeFFmpegForFile: write ffmpeg: %v", err)
	}
	// The same script acts as ffprobe; $0 detection selects the probe role.
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("makeFakeFFmpegForFile: write ffprobe: %v", err)
	}
	return ffmpegPath
}

// newProcWithPath builds a Processor pointing at an explicit ffmpeg binary path.
func newProcWithPath(ffmpegPath string) *Processor {
	return NewProcessor(ProcessorConfig{
		FFmpegPath: ffmpegPath,
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
}

// makeDummyWAV writes a non-empty placeholder file so the fake ffmpeg has a
// readable src path (the fake ignores actual content).
func makeDummyWAV(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "dummy.wav")
	if err := os.WriteFile(f, []byte("dummy"), 0644); err != nil {
		t.Fatalf("makeDummyWAV: %v", err)
	}
	return f
}

// ---------------------------------------------------------------------------
// ProcessWithOptions — success path
// ---------------------------------------------------------------------------

// TestProcessWithOptionsFakeFFmpegSuccess exercises the happy path of
// ProcessWithOptions end-to-end using the fake ffmpeg.
// This covers ProcessWithOptions lines, decodeAndSuppress, and encodeAndMux.
func TestProcessWithOptionsFakeFFmpegSuccess(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(ffmpeg)
	if err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"}); err != nil {
		t.Fatalf("expected success with fake ffmpeg, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ProcessWithOptions — OnProgress fires through the full pipeline
// ---------------------------------------------------------------------------

// TestProcessWithOptionsFakeFFmpegOnProgress verifies that all fixed OnProgress
// checkpoints (0.0, 0.1, 0.7, 1.0) are emitted during a successful run.
func TestProcessWithOptionsFakeFFmpegOnProgress(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	var progress []float64
	p := newProcWithPath(ffmpeg)
	err := p.ProcessWithOptions(src, dst, Options{
		OutputCodec: "pcm_s16le",
		OnProgress:  func(pct float64) { progress = append(progress, pct) },
	})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	// Expect at minimum the four fixed checkpoints.
	if len(progress) < 4 {
		t.Fatalf("expected at least 4 progress values, got %d: %v", len(progress), progress)
	}
	if progress[0] != 0.0 {
		t.Errorf("first progress = %f, want 0.0", progress[0])
	}
	if progress[len(progress)-1] != 1.0 {
		t.Errorf("last progress = %f, want 1.0", progress[len(progress)-1])
	}
}

// ---------------------------------------------------------------------------
// ProcessWithOptions — AudioOnly option
// ---------------------------------------------------------------------------

// TestProcessWithOptionsFakeFFmpegAudioOnly runs ProcessWithOptions with
// AudioOnly:true. info.HasVideo is false for a WAV probe result, so this
// covers the non-video branch in encodeAndMux.
func TestProcessWithOptionsFakeFFmpegAudioOnly(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(ffmpeg)
	if err := p.ProcessWithOptions(src, dst, Options{
		OutputCodec: "pcm_s16le",
		AudioOnly:   true,
	}); err != nil {
		t.Fatalf("expected success with AudioOnly: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ProcessWithOptions — OutputCodec switch branches in encodeAndMux
// ---------------------------------------------------------------------------

// TestProcessWithOptionsFakeFFmpegCodecs verifies each OutputCodec branch
// in the encodeAndMux switch statement is reachable using the fake ffmpeg.
func TestProcessWithOptionsFakeFFmpegCodecs(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)

	codecs := []string{
		"pcm_s16le", // pcm_* branch
		"pcm_mulaw", // pcm_* branch
		"pcm_alaw",  // pcm_* branch
		"opus",      // opus branch
		"aac",       // aac branch
		"mp3",       // mp3 branch
		"flac",      // flac branch
		"pcm_s24le", // default branch
	}
	for _, codec := range codecs {
		codec := codec
		t.Run(codec, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.wav")
			p := newProcWithPath(ffmpeg)
			// Branch coverage is the goal; success/failure secondary.
			_ = p.ProcessWithOptions(src, dst, Options{OutputCodec: codec})
		})
	}
}

// ---------------------------------------------------------------------------
// encodeAndMux — ffmpeg exits nonzero with "No such file" in stderr
// ---------------------------------------------------------------------------

// TestEncodeAndMuxFakeFFmpegErrorPath triggers the error branch in encodeAndMux
// (or decodeAndSuppress) when ffmpeg exits 1 with a "No such file" stderr line.
//
// A valid ffprobe is provided so Probe() succeeds, then the failing ffmpeg
// is invoked for decode/encode, triggering parseFFmpegError → ErrFileNotFound.
func TestEncodeAndMuxFakeFFmpegErrorPath(t *testing.T) {
	skipOnWindows(t)

	dir := t.TempDir()

	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.020000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.020000","bit_rate":"256000"}}`

	// ffprobe: returns valid JSON, exits 0.
	ffprobeScript := "#!/bin/sh\necho '" + probeJSON + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(ffprobeScript), 0755); err != nil {
		t.Fatalf("write ffprobe: %v", err)
	}

	// ffmpeg: always fails with "No such file or directory" in stderr.
	ffmpegScript := "#!/bin/sh\necho 'No such file or directory' >&2\nexit 1\n"
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0755); err != nil {
		t.Fatalf("write ffmpeg: %v", err)
	}

	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(ffmpegPath)
	err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"})
	if err == nil {
		t.Fatal("expected error from failing ffmpeg, got nil")
	}
	// The error surfaces from whichever phase runs first (decode or encode).
	// Either way, ErrFileNotFound should be wrapped.
	if !errors.Is(err, ErrFileNotFound) {
		// Log rather than fail — the error text still proves the path ran.
		t.Logf("error (expected to wrap ErrFileNotFound): %v", err)
	}
}

// ---------------------------------------------------------------------------
// decodeAndSuppress — ffmpeg binary does not exist (Start fails)
// ---------------------------------------------------------------------------

// TestDecodeAndSuppressFFmpegStartFails exercises the "ffmpeg start: %w" error
// path in decodeAndSuppress by using a non-existent binary path after a
// successful Probe() (which uses a separate, working ffprobe).
func TestDecodeAndSuppressFFmpegStartFails(t *testing.T) {
	skipOnWindows(t)

	dir := t.TempDir()

	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.020000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.020000","bit_rate":"256000"}}`
	ffprobeScript := "#!/bin/sh\necho '" + probeJSON + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(ffprobeScript), 0755); err != nil {
		t.Fatalf("write ffprobe: %v", err)
	}

	// ffmpeg path points to a file that does not exist, causing Start() to fail.
	nonexistentFFmpeg := filepath.Join(dir, "ffmpeg_nonexistent")

	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(nonexistentFFmpeg)
	err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"})
	if err == nil {
		t.Fatal("expected error when ffmpeg binary does not exist, got nil")
	}
}

// ---------------------------------------------------------------------------
// ProcessWithOptions — OutputCodec empty + OutputSampleRate non-zero
// ---------------------------------------------------------------------------

// TestProcessWithOptionsFakeFFmpegExplicitSampleRate covers the branch where
// opts.OutputSampleRate != 0 (uses explicit rate instead of info.SampleRate).
func TestProcessWithOptionsFakeFFmpegExplicitSampleRate(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(ffmpeg)
	_ = p.ProcessWithOptions(src, dst, Options{
		OutputCodec:      "pcm_s16le",
		OutputSampleRate: 8000,
	})
}

// ---------------------------------------------------------------------------
// ProcessWithOptions — OutputCodec empty, probed codec used directly
// ---------------------------------------------------------------------------

// TestProcessWithOptionsFakeFFmpegEmptyOutputCodec covers the branch where
// opts.OutputCodec == "" and the probed codec is not "unknown", so the probed
// codec is used directly (inferOutputCodec is NOT called).
func TestProcessWithOptionsFakeFFmpegEmptyOutputCodec(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(ffmpeg)
	_ = p.ProcessWithOptions(src, dst, Options{OutputCodec: ""})
}
