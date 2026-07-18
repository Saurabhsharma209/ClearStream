package file

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// makeVerboseStderrFakeFFmpeg builds a fake ffmpeg+ffprobe pair whose decode
// phase (last arg == "-") emits a rapid burst of "time=" progress lines on
// stderr -- right up until the moment it writes its PCM payload to stdout
// and exits, with no delay in between. This maximizes the likelihood that
// FFmpeg's stderr pipe still has unread, buffered data at the instant the
// process exits, which is exactly the condition under which calling
// decodeCmd.Wait() before draining the stderr-reading goroutine can lose
// data (violating os/exec's documented StderrPipe() contract).
//
// info.DurationSec is fixed at 10s via the ffprobe JSON below, so the last
// emitted progress line (time=00:00:09.90) should drive OnProgress to
// (nearly) the decode phase's 0.69 ceiling if -- and only if -- every
// stderr line is actually drained before decodeAndSuppress returns.
func makeVerboseStderrFakeFFmpeg(t *testing.T, numLines int) (ffmpegPath string) {
	t.Helper()
	skipOnWindows(t)
	dir := t.TempDir()

	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"10.000000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"10.000000","bit_rate":"256000"}}`

	var body string
	for i := 0; i < numLines; i++ {
		secs := float64(i) * 9.9 / float64(numLines-1)
		body += fmt.Sprintf("echo 'size=  100kB time=00:00:%05.2f bitrate=64.0kbits/s speed=1x' 1>&2\n", secs)
	}

	script := "#!/bin/sh\n" +
		"case \"$0\" in\n" +
		"  *ffprobe*)\n" +
		"    echo '" + probeJSON + "'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"for a in \"$@\"; do LAST=\"$a\"; done\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		body +
		"    dd if=/dev/zero bs=320 count=1 2>/dev/null\n" +
		"    exit 0\n" +
		"fi\n" +
		"dd if=/dev/zero of=\"$LAST\" bs=364 count=1 2>/dev/null\n" +
		"exit 0\n"

	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeVerboseStderrFakeFFmpeg: write ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("makeVerboseStderrFakeFFmpeg: write ffprobe: %v", err)
	}
	return ffmpegPath
}

// TestDecodeAndSuppressDrainsAllStderrBeforeWait is a regression test for a
// real bug flagged in DEVLOG.md (2026-07-17): decodeAndSuppress called
// decodeCmd.Wait() before draining the goroutine reading decodeCmd's
// StderrPipe(), violating os/exec's documented contract ("it is incorrect
// to call Wait before all reads from the pipe have completed"). Wait() can
// close the underlying pipe out from under a still-reading goroutine,
// truncating whatever stderr output had not yet been scanned -- silently
// dropping progress-callback data (and, per the same DEVLOG entry, a
// plausible root cause of a previously observed flaky FFmpeg-cancellation
// kill-timing test).
//
// This test drives a fake ffmpeg that writes a rapid burst of "time="
// progress lines on stderr with no delay before exiting, so that if the
// stderr goroutine were ever cut short by a premature Wait(), the final
// (highest-progress) lines would be the first casualties. It asserts both
// that processing succeeds and that OnProgress observes a decode-phase
// value close to the 0.69 ceiling, proving the full stderr stream -- right
// up to the last line before exit -- was drained.
func TestDecodeAndSuppressDrainsAllStderrBeforeWait(t *testing.T) {
	skipOnWindows(t)
	const numLines = 200
	ffmpeg := makeVerboseStderrFakeFFmpeg(t, numLines)
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

	var maxDecodeProgress float64
	decodeProgressCalls := 0
	for _, pct := range progress {
		// Decode-phase progress values live strictly inside (0.1, 0.7);
		// exclude the fixed 0.0/0.1/0.7/1.0 checkpoints emitted around it.
		if pct > 0.1 && pct < 0.7 {
			decodeProgressCalls++
			if pct > maxDecodeProgress {
				maxDecodeProgress = pct
			}
		}
	}

	if decodeProgressCalls < numLines/2 {
		t.Errorf("only %d of %d stderr progress lines produced an OnProgress call; expected most/all of them to be drained before decodeAndSuppress returned (got: %v)", decodeProgressCalls, numLines, progress)
	}
	if maxDecodeProgress < 0.68 {
		t.Errorf("max decode-phase progress = %f, want >= 0.68 (close to the 0.69 ceiling); a lower value suggests the final, highest-progress stderr lines were dropped because Wait() was called before the stderr goroutine finished draining", maxDecodeProgress)
	}
}
