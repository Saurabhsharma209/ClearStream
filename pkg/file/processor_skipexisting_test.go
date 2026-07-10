// Package file -- whitebox tests for Options.SkipExisting, covering both
// ProcessDir and ProcessDirFull. Uses package file (not file_test) so we
// can reuse the fake-ffmpeg helpers defined in processor_withffmpeg_test.go.
package file

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFileAt writes content to path and pins its mtime (and atime) to when,
// so tests can deterministically control "src is older/newer than dst"
// comparisons without racing the filesystem clock.
func writeFileAt(t *testing.T, path string, content []byte, when time.Time) {
	t.Helper()
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("writeFileAt: write %s: %v", path, err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("writeFileAt: chtimes %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// ProcessDir + SkipExisting
// ---------------------------------------------------------------------------

// TestProcessDirSkipExistingSkipsNewerDst verifies that when SkipExisting is
// true and the destination file already exists with an mtime >= the
// source's, ProcessDir does not reprocess the file: no ffmpeg job is
// launched (verified by the destination's bytes being left untouched) and
// no error is reported for it.
func TestProcessDirSkipExistingSkipsNewerDst(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	base := time.Now()
	srcPath := filepath.Join(srcDir, "already.wav")
	dstPath := filepath.Join(dstDir, "already.wav")

	writeFileAt(t, srcPath, []byte("dummy-src"), base)
	// dst is newer than src => already processed => should be skipped.
	writeFileAt(t, dstPath, []byte("previously-processed-output"), base.Add(time.Hour))

	p := newProcWithPath(ffmpeg)
	errs := p.ProcessDir(srcDir, dstDir, Options{
		OutputCodec:  "pcm_s16le",
		SkipExisting: true,
	})
	if len(errs) != 0 {
		t.Fatalf("expected no jobs (all skipped), got %d errors: %v", len(errs), errs)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "previously-processed-output" {
		t.Errorf("expected dst to be left untouched by skip, got %q", string(got))
	}
}

// TestProcessDirSkipExistingReprocessesOlderDst verifies that when
// SkipExisting is true but the destination is older than the source (a
// stale/partial output from an earlier attempt), ProcessDir still
// reprocesses the file.
func TestProcessDirSkipExistingReprocessesOlderDst(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	base := time.Now()
	srcPath := filepath.Join(srcDir, "stale.wav")
	dstPath := filepath.Join(dstDir, "stale.wav")

	// dst predates src => not yet (re)processed => must be reprocessed.
	writeFileAt(t, dstPath, []byte("stale-output"), base.Add(-time.Hour))
	writeFileAt(t, srcPath, []byte("dummy-src"), base)

	p := newProcWithPath(ffmpeg)
	errs := p.ProcessDir(srcDir, dstDir, Options{
		OutputCodec:  "pcm_s16le",
		SkipExisting: true,
	})
	for _, e := range errs {
		if e != nil {
			t.Errorf("ProcessDir error: %v", e)
		}
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) == "stale-output" {
		t.Errorf("expected dst to be overwritten by reprocessing, still has stale content")
	}
}

// TestProcessDirSkipExistingReprocessesMissingDst verifies that a missing
// destination is always (re)processed, even with SkipExisting set.
func TestProcessDirSkipExistingReprocessesMissingDst(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcPath := filepath.Join(srcDir, "new.wav")
	if err := os.WriteFile(srcPath, []byte("dummy-src"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	p := newProcWithPath(ffmpeg)
	errs := p.ProcessDir(srcDir, dstDir, Options{
		OutputCodec:  "pcm_s16le",
		SkipExisting: true,
	})
	for _, e := range errs {
		if e != nil {
			t.Errorf("ProcessDir error: %v", e)
		}
	}

	if _, err := os.Stat(filepath.Join(dstDir, "new.wav")); err != nil {
		t.Errorf("expected dst to be created, stat failed: %v", err)
	}
}

// TestProcessDirWithoutSkipExistingAlwaysReprocesses is a regression guard:
// with SkipExisting left at its default (false), a newer dst must NOT
// prevent reprocessing, preserving pre-existing behaviour.
func TestProcessDirWithoutSkipExistingAlwaysReprocesses(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	base := time.Now()
	srcPath := filepath.Join(srcDir, "already.wav")
	dstPath := filepath.Join(dstDir, "already.wav")

	writeFileAt(t, srcPath, []byte("dummy-src"), base)
	writeFileAt(t, dstPath, []byte("previously-processed-output"), base.Add(time.Hour))

	p := newProcWithPath(ffmpeg)
	errs := p.ProcessDir(srcDir, dstDir, Options{
		OutputCodec: "pcm_s16le",
		// SkipExisting intentionally left false (default).
	})
	for _, e := range errs {
		if e != nil {
			t.Errorf("ProcessDir error: %v", e)
		}
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) == "previously-processed-output" {
		t.Errorf("expected dst to be overwritten since SkipExisting is false")
	}
}

// ---------------------------------------------------------------------------
// ProcessDirFull + SkipExisting
// ---------------------------------------------------------------------------

// TestProcessDirFullSkipExistingMarksSkipReason verifies that
// ProcessDirFull reports DirResult.Skipped=true with
// SkipReason=SkipReasonAlreadyProcessed for up-to-date outputs, keeps
// SkipReasonUnsupportedExt distinct for unsupported extensions, and still
// processes (Skipped=false) files needing (re)processing.
func TestProcessDirFullSkipExistingMarksSkipReason(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	base := time.Now()

	// already processed: dst newer than src.
	writeFileAt(t, filepath.Join(srcDir, "done.wav"), []byte("dummy-src"), base)
	writeFileAt(t, filepath.Join(dstDir, "done.wav"), []byte("previously-processed-output"), base.Add(time.Hour))

	// needs reprocessing: dst older than src.
	writeFileAt(t, filepath.Join(dstDir, "stale.wav"), []byte("stale-output"), base.Add(-time.Hour))
	writeFileAt(t, filepath.Join(srcDir, "stale.wav"), []byte("dummy-src"), base)

	// needs processing: dst missing entirely.
	if err := os.WriteFile(filepath.Join(srcDir, "new.wav"), []byte("dummy-src"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// unsupported extension: always skipped, regardless of SkipExisting.
	if err := os.WriteFile(filepath.Join(srcDir, "notes.txt"), []byte("text"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	p := newProcWithPath(ffmpeg)
	results := p.ProcessDirFull(srcDir, dstDir, Options{
		OutputCodec:  "pcm_s16le",
		SkipExisting: true,
	})

	byName := make(map[string]DirResult, len(results))
	for _, r := range results {
		byName[filepath.Base(r.Src)] = r
	}

	done, ok := byName["done.wav"]
	if !ok {
		t.Fatalf("missing result for done.wav")
	}
	if !done.Skipped || done.SkipReason != SkipReasonAlreadyProcessed {
		t.Errorf("done.wav: expected Skipped=true, SkipReason=%q, got Skipped=%v, SkipReason=%q",
			SkipReasonAlreadyProcessed, done.Skipped, done.SkipReason)
	}
	if done.Err != nil {
		t.Errorf("done.wav: expected no error for a skipped file, got %v", done.Err)
	}

	stale, ok := byName["stale.wav"]
	if !ok {
		t.Fatalf("missing result for stale.wav")
	}
	if stale.Skipped {
		t.Errorf("stale.wav: expected Skipped=false (reprocessed), got Skipped=true, reason=%q", stale.SkipReason)
	}
	if stale.Err != nil {
		t.Errorf("stale.wav: unexpected processing error: %v", stale.Err)
	}

	newf, ok := byName["new.wav"]
	if !ok {
		t.Fatalf("missing result for new.wav")
	}
	if newf.Skipped {
		t.Errorf("new.wav: expected Skipped=false (no prior dst), got Skipped=true, reason=%q", newf.SkipReason)
	}

	notes, ok := byName["notes.txt"]
	if !ok {
		t.Fatalf("missing result for notes.txt")
	}
	if !notes.Skipped || notes.SkipReason != SkipReasonUnsupportedExt {
		t.Errorf("notes.txt: expected Skipped=true, SkipReason=%q, got Skipped=%v, SkipReason=%q",
			SkipReasonUnsupportedExt, notes.Skipped, notes.SkipReason)
	}

	// Sanity: the two skip reasons must remain distinguishable.
	if SkipReasonUnsupportedExt == SkipReasonAlreadyProcessed {
		t.Fatalf("SkipReasonUnsupportedExt and SkipReasonAlreadyProcessed must be distinct values")
	}
}

// TestProcessDirFullSkipReasonEmptyWhenProcessed verifies SkipReason stays
// empty for files that are actually processed (Skipped=false), so existing
// callers that only check .Skipped keep seeing accurate, unambiguous state.
func TestProcessDirFullSkipReasonEmptyWhenProcessed(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "clip.wav"), []byte("dummy-src"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	p := newProcWithPath(ffmpeg)
	results := p.ProcessDirFull(srcDir, dstDir, Options{
		OutputCodec:  "pcm_s16le",
		SkipExisting: true,
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Skipped {
		t.Errorf("expected clip.wav to be processed, not skipped")
	}
	if results[0].SkipReason != "" {
		t.Errorf("expected empty SkipReason for a processed file, got %q", results[0].SkipReason)
	}
	if results[0].Err != nil {
		t.Errorf("unexpected processing error: %v", results[0].Err)
	}
}

// ---------------------------------------------------------------------------
// isAlreadyProcessed unit tests
// ---------------------------------------------------------------------------

func TestIsAlreadyProcessed(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()

	src := filepath.Join(dir, "src.wav")
	dstNewer := filepath.Join(dir, "dst_newer.wav")
	dstOlder := filepath.Join(dir, "dst_older.wav")
	dstEqual := filepath.Join(dir, "dst_equal.wav")
	dstMissing := filepath.Join(dir, "does_not_exist.wav")

	writeFileAt(t, src, []byte("s"), base)
	writeFileAt(t, dstNewer, []byte("d"), base.Add(time.Minute))
	writeFileAt(t, dstOlder, []byte("d"), base.Add(-time.Minute))
	writeFileAt(t, dstEqual, []byte("d"), base)

	cases := []struct {
		name string
		dst  string
		want bool
	}{
		{"dst newer than src", dstNewer, true},
		{"dst equal to src", dstEqual, true},
		{"dst older than src", dstOlder, false},
		{"dst missing", dstMissing, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAlreadyProcessed(src, tc.dst); got != tc.want {
				t.Errorf("isAlreadyProcessed(src, %s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}

	if isAlreadyProcessed(filepath.Join(dir, "no_such_src.wav"), dstNewer) {
		t.Errorf("expected false when src is missing")
	}
}
