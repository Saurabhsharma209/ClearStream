package eval

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skipOnWindowsEval(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-ffmpeg shell script not supported on Windows")
	}
}

func makeFakeFFmpegForEval(t *testing.T) string {
	t.Helper()
	skipOnWindowsEval(t)
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do LAST=\"$a\"; done\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		"    dd if=/dev/zero bs=3200 count=1 2>/dev/null\n" +
		"    exit 0\n" +
		"fi\n" +
		"exit 0\n"
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func makeFakeFFmpegEmptyOutput(t *testing.T) string {
	t.Helper()
	skipOnWindowsEval(t)
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func touchFileEval(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

// TestBatchRunner_RunWithFakeFFmpeg exercises BatchRunner.Run end-to-end.
func TestBatchRunner_RunWithFakeFFmpeg(t *testing.T) {
	skipOnWindowsEval(t)
	ffmpeg := makeFakeFFmpegForEval(t)
	inDir := t.TempDir()
	outDir := t.TempDir()
	touchFileEval(t, filepath.Join(inDir, "sample.wav"))
	r := NewBatchRunner(BatchConfig{
		InputDir:   inDir,
		OutputDir:  outDir,
		Suppressor: &passthroughSuppressor{},
		FFmpegPath: ffmpeg,
		Workers:    1,
	})
	summary, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(summary.Files) != 1 {
		t.Fatalf("want 1 file result, got %d", len(summary.Files))
	}
	if summary.Files[0].Error != "" {
		t.Errorf("unexpected file error: %s", summary.Files[0].Error)
	}
}

// TestBatchRunner_RunOnProgress verifies OnProgress is called with correct totals.
func TestBatchRunner_RunOnProgress(t *testing.T) {
	skipOnWindowsEval(t)
	ffmpeg := makeFakeFFmpegForEval(t)
	inDir := t.TempDir()
	outDir := t.TempDir()
	touchFileEval(t, filepath.Join(inDir, "a.wav"))
	var lastDone, lastTotal int
	r := NewBatchRunner(BatchConfig{
		InputDir:   inDir,
		OutputDir:  outDir,
		Suppressor: &passthroughSuppressor{},
		FFmpegPath: ffmpeg,
		Workers:    1,
		OnProgress: func(done, total int) { lastDone, lastTotal = done, total },
	})
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lastDone != 1 || lastTotal != 1 {
		t.Errorf("OnProgress: want done=1 total=1, got done=%d total=%d", lastDone, lastTotal)
	}
}

// TestBatchRunner_RunContextCancel verifies Run handles a pre-cancelled context.
func TestBatchRunner_RunContextCancel(t *testing.T) {
	skipOnWindowsEval(t)
	ffmpeg := makeFakeFFmpegForEval(t)
	inDir := t.TempDir()
	outDir := t.TempDir()
	for _, name := range []string{"a.wav", "b.wav", "c.wav"} {
		touchFileEval(t, filepath.Join(inDir, name))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := NewBatchRunner(BatchConfig{
		InputDir:   inDir,
		OutputDir:  outDir,
		Suppressor: &passthroughSuppressor{},
		FFmpegPath: ffmpeg,
	})
	if _, err := r.Run(ctx); err != nil {
		t.Logf("Run with cancelled ctx returned: %v", err)
	}
}

// TestBatchRunner_RunOutputDirCreation verifies Run creates OutputDir if absent.
func TestBatchRunner_RunOutputDirCreation(t *testing.T) {
	skipOnWindowsEval(t)
	ffmpeg := makeFakeFFmpegForEval(t)
	inDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "new", "subdir")
	touchFileEval(t, filepath.Join(inDir, "x.wav"))
	r := NewBatchRunner(BatchConfig{
		InputDir:   inDir,
		OutputDir:  outDir,
		Suppressor: &passthroughSuppressor{},
		FFmpegPath: ffmpeg,
		Workers:    1,
	})
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(outDir); err != nil {
		t.Errorf("OutputDir was not created: %v", err)
	}
}

// TestDecodeToRawPCM_FakeFFmpeg exercises decodeToRawPCM directly.
func TestDecodeToRawPCM_FakeFFmpeg(t *testing.T) {
	skipOnWindowsEval(t)
	ffmpeg := makeFakeFFmpegForEval(t)
	f, err := os.CreateTemp(t.TempDir(), "input*.wav")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()
	pcm, sampleRate, durationMs, err := decodeToRawPCM(context.Background(), ffmpeg, f.Name(), 16000)
	if err != nil {
		t.Fatalf("decodeToRawPCM: %v", err)
	}
	if len(pcm) == 0 {
		t.Error("expected non-empty PCM output")
	}
	if sampleRate != 16000 {
		t.Errorf("sampleRate: want 16000, got %d", sampleRate)
	}
	if durationMs <= 0 {
		t.Errorf("durationMs: want > 0, got %f", durationMs)
	}
}

// TestDecodeToRawPCM_FFmpegFails verifies error when ffmpeg binary is missing.
func TestDecodeToRawPCM_FFmpegFails(t *testing.T) {
	_, _, _, err := decodeToRawPCM(context.Background(), "/nonexistent/ffmpeg", "/tmp/dummy.wav", 16000)
	if err == nil {
		t.Error("expected error for non-existent ffmpeg binary")
	}
}

// TestDecodeToRawPCM_EmptyOutput verifies error when ffmpeg writes no bytes.
func TestDecodeToRawPCM_EmptyOutput(t *testing.T) {
	skipOnWindowsEval(t)
	ffmpeg := makeFakeFFmpegEmptyOutput(t)
	f, err := os.CreateTemp(t.TempDir(), "input*.wav")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()
	_, _, _, err = decodeToRawPCM(context.Background(), ffmpeg, f.Name(), 16000)
	if err == nil {
		t.Error("expected error for empty ffmpeg output")
	}
}
