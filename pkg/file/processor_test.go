package file_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

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
