// Package file – tests for encodeAndMux's atomic-promote-on-success
// behaviour: FFmpeg's encode output must land in a temp file first and
// only replace dst via os.Rename once the encode fully succeeds, so a
// failed (or cancelled) encode can never leave a truncated/corrupt file
// at dst, and can never clobber a pre-existing dst from an earlier
// successful run.
package file

import (
	"os"
	"path/filepath"
	"testing"
)

// makeFailingEncodeFakeFFmpeg builds a fake ffmpeg+ffprobe pair whose decode
// phase (last arg == "-") succeeds, but whose encode phase (writing to the
// last argument) writes some bytes to that path and then exits non-zero --
// simulating a real FFmpeg encoder failure (e.g. a bad bitrate or corrupt
// PCM) that still leaves partial output on disk before failing.
func makeFailingEncodeFakeFFmpeg(t *testing.T) (ffmpegPath string) {
	t.Helper()
	skipOnWindows(t)
	dir := t.TempDir()
	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.020000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.020000","bit_rate":"256000"}}`
	script := "#!/bin/sh\n" +
		"case \"$0\" in\n" +
		"  *ffprobe*)\n" +
		"    echo '" + probeJSON + "'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"for a in \"$@\"; do LAST=\"$a\"; done\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		"    dd if=/dev/zero bs=320 count=1 2>/dev/null\n" +
		"    exit 0\n" +
		"fi\n" +
		// Encode phase: write some garbage to the output path (whatever
		// that path is -- the temp file, with the fix in place), then
		// fail, mimicking a real encoder that emits a partial file before
		// erroring out.
		"echo 'garbage partial output' > \"$LAST\"\n" +
		"echo 'encode boom' >&2\n" +
		"exit 1\n"
	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeFailingEncodeFakeFFmpeg: write ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("makeFailingEncodeFakeFFmpeg: write ffprobe: %v", err)
	}
	return ffmpegPath
}

// TestEncodeAndMuxFailureLeavesNoPartialDst verifies that a failing encode
// never leaves a truncated/partial file at dst: the fake ffmpeg above
// writes garbage to its output-path argument before failing, and with
// encodeAndMux writing to a temp file (promoted to dst only on success)
// that garbage must land in a temp file, not dst, and the temp file must
// be cleaned up afterward.
func TestEncodeAndMuxFailureLeavesNoPartialDst(t *testing.T) {
	ffmpeg := makeFailingEncodeFakeFFmpeg(t)
	src := makeDummyWAV(t)
	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "out.wav")

	p := newProcWithPath(ffmpeg)
	err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"})
	if err == nil {
		t.Fatal("expected error from failing encode, got nil")
	}

	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("expected dst to not exist after failed encode, stat err: %v", statErr)
	}

	entries, readErr := os.ReadDir(dstDir)
	if readErr != nil {
		t.Fatalf("read dst dir: %v", readErr)
	}
	for _, e := range entries {
		t.Errorf("expected dst dir to be empty after failed encode (no leaked temp file), found: %s", e.Name())
	}
}

// TestEncodeAndMuxFailurePreservesExistingDst verifies that when dst
// already exists (e.g. from a prior successful run), a subsequent failed
// re-encode leaves that existing dst completely untouched rather than
// truncating/overwriting it with partial output.
func TestEncodeAndMuxFailurePreservesExistingDst(t *testing.T) {
	ffmpeg := makeFailingEncodeFakeFFmpeg(t)
	src := makeDummyWAV(t)
	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "out.wav")

	const original = "previously encoded output"
	if err := os.WriteFile(dst, []byte(original), 0644); err != nil {
		t.Fatalf("seed existing dst: %v", err)
	}

	p := newProcWithPath(ffmpeg)
	err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"})
	if err == nil {
		t.Fatal("expected error from failing encode, got nil")
	}

	got, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read dst after failed encode: %v", readErr)
	}
	if string(got) != original {
		t.Errorf("existing dst was modified by a failed encode: got %q, want unchanged %q", got, original)
	}

	entries, readDirErr := os.ReadDir(dstDir)
	if readDirErr != nil {
		t.Fatalf("read dst dir: %v", readDirErr)
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly the original dst file in dst dir, found %d entries", len(entries))
	}
}
