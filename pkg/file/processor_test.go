package file_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// requireFFmpeg skips the test if ffmpeg is not in PATH.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH — skipping test that requires it")
	}
}

func newTestProcessor() *file.Processor {
	return file.NewProcessor(file.ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
}

func TestProcessDirEmptyDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	p := newTestProcessor()
	errs := p.ProcessDir(src, dst, file.Options{})
	if len(errs) != 0 {
		t.Errorf("expected no errors for empty dir, got %v", errs)
	}
}

func TestProcessDirNonExistent(t *testing.T) {
	p := newTestProcessor()
	errs := p.ProcessDir("/nonexistent/path/xyz", t.TempDir(), file.Options{})
	if len(errs) == 0 {
		t.Error("expected error for nonexistent src dir")
	}
}

func TestTypedErrors(t *testing.T) {
	p := newTestProcessor()
	err := p.Process("/nonexistent/file.wav", filepath.Join(t.TempDir(), "out.wav"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestOptionsOnProgress(t *testing.T) {
	called := false
	opts := file.Options{
		OnProgress: func(pct float64) { called = true },
	}
	_ = opts
	// OnProgress is wired in but only called when ffmpeg is available and file exists.
	// This test verifies the struct field compiles correctly.
	if called {
		t.Error("should not have been called without processing")
	}
}

func TestExportedErrors(t *testing.T) {
	// Verify typed errors are exported and non-nil
	if file.ErrCodecNotFound == nil {
		t.Error("ErrCodecNotFound should not be nil")
	}
	if file.ErrFileNotFound == nil {
		t.Error("ErrFileNotFound should not be nil")
	}
	if file.ErrPermission == nil {
		t.Error("ErrPermission should not be nil")
	}
}

func TestProcessDirCreatesDestDir(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "newsubdir")
	p := newTestProcessor()
	errs := p.ProcessDir(src, dst, file.Options{})
	if len(errs) != 0 {
		t.Errorf("expected no errors for empty dir, got %v", errs)
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Error("expected dst dir to be created")
	}
}

func TestStreamProcess(t *testing.T) {
	// Synthetic PCM: 10 frames of silence (all zeros)
	frameCount := 10
	inputPCM := make([]byte, audio.FrameSizeBytes*frameCount)

	r := bytes.NewReader(inputPCM)
	var w bytes.Buffer

	opts := file.Options{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	}

	if err := file.StreamProcess(context.Background(), r, &w, opts); err != nil {
		t.Fatalf("StreamProcess failed: %v", err)
	}

	if w.Len() != len(inputPCM) {
		t.Errorf("output length %d != input length %d", w.Len(), len(inputPCM))
	}
}

func BenchmarkStreamProcess(b *testing.B) {
	frames := b.N * 160
	samples := make([]int16, frames)
	for i := range samples {
		t := float64(i) / 16000.0
		samples[i] = int16(8000 * math.Sin(2*math.Pi*440*t))
	}
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}

	opts := file.Options{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	}

	b.SetBytes(int64(frames * 320))
	b.ResetTimer()

	r := bytes.NewReader(buf)
	var w bytes.Buffer
	if err := file.StreamProcess(context.Background(), r, &w, opts); err != nil {
		b.Fatalf("StreamProcess failed: %v", err)
	}
}

func TestStreamProcessLargeInput(t *testing.T) {
	frames := 1000
	inputPCM := make([]byte, audio.FrameSizeBytes*frames)

	r := bytes.NewReader(inputPCM)
	var w bytes.Buffer

	opts := file.Options{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	}

	if err := file.StreamProcess(context.Background(), r, &w, opts); err != nil {
		t.Fatalf("StreamProcess failed: %v", err)
	}

	if w.Len() != len(inputPCM) {
		t.Errorf("output length %d != input length %d", w.Len(), len(inputPCM))
	}
}

func TestErrFileNotFoundWrapping(t *testing.T) {
	requireFFmpeg(t)
	p := newTestProcessor()
	err := p.Process("/tmp/clearstream_nonexistent_12345.wav", filepath.Join(t.TempDir(), "out.wav"))
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
	if !errors.Is(err, file.ErrFileNotFound) {
		t.Errorf("expected error to wrap ErrFileNotFound, got: %v", err)
	}
}

func TestProcessDirSkipsUnsupportedExtensions(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	for _, name := range []string{"test.wav", "test.mp3", "test.txt", "test.pdf", "test.json"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", name, err)
		}
	}

	p := newTestProcessor()
	results := p.ProcessDirFull(src, dst, file.Options{Suppressor: model.NewPassthrough(), Logger: zap.NewNop()})

	skipped := 0
	notSkipped := 0
	for _, r := range results {
		if r.Skipped {
			skipped++
		} else {
			notSkipped++
		}
	}

	if skipped != 3 {
		t.Errorf("expected 3 skipped files (.txt, .pdf, .json), got %d", skipped)
	}
	if notSkipped != 2 {
		t.Errorf("expected 2 non-skipped files (.wav, .mp3), got %d", notSkipped)
	}
}

func TestProcessDirCreatesOutputDir(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "output", "nested")

	if err := os.WriteFile(filepath.Join(src, "test.wav"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	p := newTestProcessor()
	p.ProcessDir(src, dst, file.Options{Suppressor: model.NewPassthrough(), Logger: zap.NewNop()})

	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Error("expected dstDir to be created, but it does not exist")
	}
}

func TestStreamProcessContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inputPCM := make([]byte, audio.FrameSizeBytes*100)
	r := bytes.NewReader(inputPCM)
	var w bytes.Buffer

	opts := file.Options{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	}

	err := file.StreamProcess(ctx, r, &w, opts)
	if err == nil {
		t.Error("expected error from StreamProcess with cancelled context, got nil")
	}
}
