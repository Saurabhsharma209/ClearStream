package file

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// encodeAndMux — error paths specific to the ENCODE phase
// ---------------------------------------------------------------------------
//
// TestEncodeAndMuxFakeFFmpegErrorPath (processor_cancel_test.go's sibling,
// processor_withffmpeg_test.go) already exercises a failing ffmpeg, but its
// own comment notes the error "surfaces from whichever phase runs first
// (decode or encode)" -- meaning encodeAndMux's own typed-error and
// generic-error branches were never proven to trigger specifically from the
// encode invocation (decode always runs first and was failing in that test).
// These two tests force the DECODE phase to succeed and only the ENCODE
// phase to fail, isolating encodeAndMux's own error handling.

// makeFakeFFmpegDecodeOKEncodeFails writes a fake ffmpeg that succeeds for
// ffprobe and for the decode invocation (identified by its last arg being
// "-", i.e. writing raw PCM to stdout), but fails the encode invocation
// (last arg is a real destination path) with encodeStderr on stderr.
func makeFakeFFmpegDecodeOKEncodeFails(t *testing.T, encodeStderr string) (ffmpegPath string) {
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
		"echo '" + encodeStderr + "' >&2\n" +
		"exit 1\n"

	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("write ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("write ffprobe: %v", err)
	}
	return ffmpegPath
}

// TestEncodeAndMuxTypedErrorFromEncodePhase proves that when the ENCODE
// invocation (not decode) fails with a recognizable ffmpeg stderr pattern
// ("unknown encoder" -> ErrCodecNotFound via parseFFmpegError), encodeAndMux
// wraps and returns that typed error rather than a generic one.
func TestEncodeAndMuxTypedErrorFromEncodePhase(t *testing.T) {
	ffmpeg := makeFakeFFmpegDecodeOKEncodeFails(t, "Unknown encoder 'libopus'")
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.opus")

	p := newProcWithPath(ffmpeg)
	err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "opus"})
	if err == nil {
		t.Fatal("expected error when encode phase fails, got nil")
	}
	if !errors.Is(err, ErrCodecNotFound) {
		t.Errorf("expected error to wrap ErrCodecNotFound, got: %v", err)
	}
}

// TestEncodeAndMuxGenericErrorFromEncodePhase proves that when the ENCODE
// invocation fails with a stderr message that doesn't match any of
// parseFFmpegError's known patterns, encodeAndMux falls back to a generic
// wrapped error that still surfaces the ffmpeg stderr for diagnosis.
func TestEncodeAndMuxGenericErrorFromEncodePhase(t *testing.T) {
	const weirdStderr = "Muxer core dumped on frame 42"
	ffmpeg := makeFakeFFmpegDecodeOKEncodeFails(t, weirdStderr)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	p := newProcWithPath(ffmpeg)
	err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"})
	if err == nil {
		t.Fatal("expected error when encode phase fails, got nil")
	}
	if errors.Is(err, ErrCodecNotFound) || errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrPermission) {
		t.Errorf("expected an untyped/generic error for unrecognized stderr, got typed error: %v", err)
	}
	if !strings.Contains(err.Error(), "ffmpeg encode") {
		t.Errorf("expected generic error to mention \"ffmpeg encode\", got: %v", err)
	}
	if !strings.Contains(err.Error(), weirdStderr) {
		t.Errorf("expected generic error to surface ffmpeg stderr %q, got: %v", weirdStderr, err)
	}
}
