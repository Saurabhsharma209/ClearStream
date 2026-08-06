// Package file -- regression test for a decodeAndSuppress stdout-pipe deadlock.
//
// decodeAndSuppress pipes FFmpeg's stdout through an unbuffered io.Pipe (pr/pw)
// into a reader goroutine that suppresses and writes PCM. Because decodeCmd.Stdout
// is set to pw (not an *os.File), os/exec internally routes FFmpeg's real stdout
// through its own copy goroutine into pw, and Cmd.Wait() blocks on that internal
// goroutine finishing. If the reader goroutine stops calling pr.Read() as soon as
// it hits a processing error -- instead of draining the rest of pr -- any
// still-queued FFmpeg output beyond what was already read permanently blocks that
// internal copy goroutine's write, so decodeCmd.Wait() (and the whole call, and
// the ffmpeg child process) hangs forever. This test reproduces that scenario with
// a fake ffmpeg that emits far more PCM than a single read absorbs, paired with a
// suppressor that fails immediately, and bounds the wait so a regression shows up
// as a test failure instead of an actual indefinite hang.
package file

import (
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestProcessWithOptionsSuppressorErrorDoesNotHangFFmpeg(t *testing.T) {
	skipOnWindows(t)

	// Comfortably exceeds the reader goroutine's 64-frame (20480 byte) read
	// buffer, any OS pipe buffering, and io.Copy's internal buffer, so
	// unconsumed FFmpeg output remains queued after the first read.
	const numSamples = 300000 // ~586KB of PCM
	ffmpeg := makeFakeFFmpegConstAmplitude(t, 500, numSamples)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.pcm")

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: ffmpeg,
		SampleRate: 16000,
		Channels:   1,
		Suppressor: &failingSuppressor{},
		Logger:     zap.NewNop(),
	})

	done := make(chan error, 1)
	go func() {
		done <- p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from the failing suppressor, got nil")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ProcessWithOptions hung: decodeAndSuppress did not return after a mid-stream suppressor error with a large FFmpeg output backlog (stdout pipe deadlock)")
	}
}
