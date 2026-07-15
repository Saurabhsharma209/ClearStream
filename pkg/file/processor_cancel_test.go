// Package file – tests for Options.Context cancellation support: verifies
// that ProcessWithOptions (and by extension ProcessDir/ProcessDirFull, which
// share the same code path per-file) both short-circuit before ever
// invoking FFmpeg when the context is already cancelled, and kill an
// in-flight FFmpeg child process promptly when the context is cancelled
// mid-decode or mid-encode, rather than running the process to completion.
package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// makeSlowDecodeFakeFFmpeg builds a fake ffmpeg+ffprobe pair whose decode
// phase (last arg == "-") sleeps for sleepSecs before producing any output.
// This gives tests a reliable window to cancel a context mid-decode and
// observe whether the FFmpeg child process is actually killed, rather than
// abandoned to run to completion in the background.
func makeSlowDecodeFakeFFmpeg(t *testing.T, sleepSecs int) (ffmpegPath string) {
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
		"    sleep " + strconv.Itoa(sleepSecs) + "\n" +
		"    dd if=/dev/zero bs=320 count=1 2>/dev/null\n" +
		"    exit 0\n" +
		"fi\n" +
		"dd if=/dev/zero of=\"$LAST\" bs=364 count=1 2>/dev/null\n" +
		"exit 0\n"

	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeSlowDecodeFakeFFmpeg: write ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("makeSlowDecodeFakeFFmpeg: write ffprobe: %v", err)
	}
	return ffmpegPath
}

// makeSlowEncodeFakeFFmpeg is like makeSlowDecodeFakeFFmpeg but the decode
// phase returns immediately and the encode phase (writing to the last
// argument) sleeps for sleepSecs first.
func makeSlowEncodeFakeFFmpeg(t *testing.T, sleepSecs int) (ffmpegPath string) {
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
		"sleep " + strconv.Itoa(sleepSecs) + "\n" +
		"dd if=/dev/zero of=\"$LAST\" bs=364 count=1 2>/dev/null\n" +
		"exit 0\n"

	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeSlowEncodeFakeFFmpeg: write ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("makeSlowEncodeFakeFFmpeg: write ffprobe: %v", err)
	}
	return ffmpegPath
}

// TestProcessWithOptionsPreCancelledContext verifies that a pre-cancelled
// Options.Context makes ProcessWithOptions fail fast with context.Canceled
// without ever invoking FFmpeg (the fake decode step would sleep 5s if it
// were actually run).
func TestProcessWithOptionsPreCancelledContext(t *testing.T) {
	ffmpeg := makeSlowDecodeFakeFFmpeg(t, 5)
	src := makeDummyWAV(t)
	dst := filepath.Join(t.TempDir(), "out.wav")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newProcWithPath(ffmpeg)
	start := time.Now()
	err := p.ProcessWithOptions(src, dst, Options{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
		Context:    ctx,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from ProcessWithOptions with pre-cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("ProcessWithOptions took %v with pre-cancelled context; expected a near-instant short-circuit (fake ffmpeg would sleep 5s if it were invoked)", elapsed)
	}
}

// TestProcessWithOptionsContextCancelKillsRunningFFmpegDuringDecode verifies
// that cancelling Options.Context while the decode-phase FFmpeg process is
// running actually kills that process (via exec.CommandContext) instead of
// letting it run to completion in the background.
func TestProcessWithOptionsContextCancelKillsRunningFFmpegDuringDecode(t *testing.T) {
	ffmpeg := makeSlowDecodeFakeFFmpeg(t, 5)
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
		t.Fatal("expected error from ProcessWithOptions when context is cancelled mid-decode, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("ProcessWithOptions took %v after cancellation; expected the running fake-ffmpeg (5s decode sleep) to be killed promptly rather than run to completion", elapsed)
	}
}

// TestProcessWithOptionsContextCancelKillsRunningFFmpegDuringEncode is like
// the decode-phase test above but cancels while the encode-phase FFmpeg
// process is running, exercising encodeAndMux's independent
// exec.CommandContext + ctx.Err() handling.
func TestProcessWithOptionsContextCancelKillsRunningFFmpegDuringEncode(t *testing.T) {
	ffmpeg := makeSlowEncodeFakeFFmpeg(t, 5)
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
		Suppressor:  model.NewPassthrough(),
		Logger:      zap.NewNop(),
		OutputCodec: "pcm_s16le",
		Context:     ctx,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from ProcessWithOptions when context is cancelled mid-encode, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("ProcessWithOptions took %v after cancellation during encode; expected the running fake-ffmpeg (5s encode sleep) to be killed promptly", elapsed)
	}
}

// TestProcessDirContextCancellationStopsQueuedJobs verifies that cancelling
// Options.Context during a ProcessDir batch run kills the in-flight file's
// FFmpeg process and causes queued-but-not-yet-started files to fail fast
// with context.Canceled instead of each running their own multi-second
// FFmpeg invocation to completion.
func TestProcessDirContextCancellationStopsQueuedJobs(t *testing.T) {
	ffmpeg := makeSlowDecodeFakeFFmpeg(t, 5)
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	for i := 0; i < 4; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("track%d.wav", i))
		if err := os.WriteFile(name, []byte("dummy"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	p := newProcWithPath(ffmpeg)
	start := time.Now()
	errs := p.ProcessDir(srcDir, dstDir, Options{
		MaxConcurrency: 1,
		Context:        ctx,
	})
	elapsed := time.Since(start)

	if len(errs) != 4 {
		t.Fatalf("expected 4 results, got %d", len(errs))
	}
	sawCancel := false
	for _, e := range errs {
		if errors.Is(e, context.Canceled) {
			sawCancel = true
		}
	}
	if !sawCancel {
		t.Errorf("expected at least one ProcessDir result to be context.Canceled, got: %v", errs)
	}
	// With MaxConcurrency=1 and a 5s-sleeping fake ffmpeg per file, an
	// uncancelled run would take roughly 20s (4 files x 5s). Cancellation
	// should cut this off dramatically once the in-flight job's ffmpeg
	// process is killed and queued jobs fast-fail via ctx.Err().
	if elapsed > 8*time.Second {
		t.Errorf("ProcessDir took %v after cancellation; expected queued jobs to short-circuit instead of running to completion (would be ~20s uncancelled)", elapsed)
	}
}
