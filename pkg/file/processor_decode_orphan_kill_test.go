// Package file -- regression coverage for decodeAndSuppress's cancellation
// watcher goroutine actually killing an orphaned grandchild process, not
// just the direct FFmpeg child.
//
// decodeAndSuppress launches a goroutine that calls killProcessGroup(cmd)
// when ctx is cancelled, specifically because exec.CommandContext's own
// built-in cancellation only kills the *direct* child process -- if that
// child has already forked and exited (leaving an orphaned grandchild that
// inherited the stdout pipe fd), the stdlib kill does nothing to unblock
// the pipe reader. Every existing cancellation test uses a fake ffmpeg that
// itself sleeps and is still alive (and therefore killable directly) when
// ctx is cancelled, so this specific process-group-kill branch was never
// exercised. This test uses a fake ffmpeg whose shell process backgrounds a
// long-sleeping grandchild (inheriting the stdout pipe) and exits
// immediately, so only a real process-group signal -- not a single-process
// kill -- can unblock decodeAndSuppress promptly.
package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// makeOrphaningDecodeFakeFFmpeg builds a fake ffmpeg+ffprobe pair whose
// decode phase (last arg == "-") backgrounds a sleepSecs-long grandchild
// that inherits the stdout pipe file descriptor, then has the direct child
// exit right away -- leaving the grandchild as an orphan still holding the
// pipe open.
func makeOrphaningDecodeFakeFFmpeg(t *testing.T, sleepSecs int) (ffmpegPath string) {
	t.Helper()
	skipOnWindows(t)
	dir := t.TempDir()
	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.020000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.020000","bit_rate":"256000"}}`
	script := fmt.Sprintf(`#!/bin/sh
case "$0" in
  *ffprobe*)
    echo '%s'
    exit 0
    ;;
esac
for a in "$@"; do LAST="$a"; done
if [ "$LAST" = "-" ]; then
    (sleep %d; dd if=/dev/zero bs=320 count=1 2>/dev/null) &
    exit 0
fi
dd if=/dev/zero of="$LAST" bs=364 count=1 2>/dev/null
exit 0
`, probeJSON, sleepSecs)
	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeOrphaningDecodeFakeFFmpeg: write ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("makeOrphaningDecodeFakeFFmpeg: write ffprobe: %v", err)
	}
	return ffmpegPath
}

// TestProcessWithOptionsContextCancelKillsOrphanedDecodeGrandchild verifies
// that cancelling Options.Context mid-decode kills an orphaned grandchild
// process holding the stdout pipe open, via decodeAndSuppress's
// killProcessGroup call, rather than hanging until that grandchild's own
// sleep expires.
func TestProcessWithOptionsContextCancelKillsOrphanedDecodeGrandchild(t *testing.T) {
	ffmpeg := makeOrphaningDecodeFakeFFmpeg(t, 20)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	p := newProcWithPath(ffmpeg)
	start := time.Now()
	err := p.ProcessWithOptions(src, dst, Options{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
		Context:    ctx,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from ProcessWithOptions when context is cancelled mid-decode, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("ProcessWithOptions took %v after cancellation; expected the orphaned grandchild holding the stdout pipe open to be killed via killProcessGroup instead of outliving its 20s sleep", elapsed)
	}
}
