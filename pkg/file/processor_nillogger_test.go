// Package file -- regression test for NewProcessor with an unset Logger.
//
// ProcessorConfig.Logger has no doc requirement forcing callers to set it
// (unlike, say, Suppressor, which ProcessWithOptions explicitly needs).
// NewProcessor previously stored whatever ProcessorConfig it was given
// verbatim, and ProcessWithOptions called p.cfg.Logger.With(...)
// unconditionally with no nil guard -- so a Processor built without an
// explicit Logger crashed with a nil pointer dereference (panic, not an
// error return) on the very first call, before touching any file. Every
// existing test in this package happens to always pass an explicit Logger
// (usually zap.NewNop()), so this was never exercised. Fixed by having
// NewProcessor default a nil Logger to zap.NewNop(), mirroring the
// already-nil-safe pattern StreamProcess uses for Options.Logger.
package file

import (
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

// TestNewProcessorNilLoggerDoesNotPanic drives ProcessWithOptions through a
// Processor built with a zero-value ProcessorConfig.Logger. Pre-fix, this
// panicked inside ProcessWithOptions's first logger.With(...) call, before
// the missing-source-file check even ran; the source path here is
// deliberately nonexistent so the only thing under test is whether we get a
// clean typed error back (ErrFileNotFound) instead of a panic.
func TestNewProcessorNilLoggerDoesNotPanic(t *testing.T) {
	supp, err := model.NewSuppressor(model.SuppressorConfig{Backend: "passthrough"})
	if err != nil {
		t.Fatalf("new suppressor: %v", err)
	}
	defer supp.Close()

	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Suppressor: supp,
		// Logger deliberately left nil.
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ProcessWithOptions panicked with nil Logger: %v", r)
		}
	}()

	err = p.ProcessWithOptions("/tmp/cs_nonexistent_nillogger_src.wav", "/tmp/cs_nonexistent_nillogger_dst.wav", Options{})
	if err == nil {
		t.Fatal("expected an error for a nonexistent source file, got nil")
	}
}
