package billing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeCDR is a helper that returns a simple CDR for testing.
func makeCDR(sessionID string) CDR {
	start := time.Now()
	end := start.Add(7 * time.Second)
	return NewCDR(sessionID, "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)
}

// TestWALWriter_NewWALWriter_MkdirFails passes a path whose parent is a file,
// so os.MkdirAll cannot create it — asserts error is non-nil.
func TestWALWriter_NewWALWriter_MkdirFails(t *testing.T) {
	// Create a regular file.
	f, err := os.CreateTemp("", "billing-wal-notadir-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	// Use the file as if it were a directory — MkdirAll must fail.
	impossibleDir := filepath.Join(f.Name(), "subdir")
	_, err = NewWALWriter(impossibleDir, nil)
	if err == nil {
		t.Fatal("expected error when dir cannot be created, got nil")
	}
}

// TestWALWriter_Write_TriggersRotation sets RotateInterval = -1 so that
// time.Since(created) >= interval is always true, forcing rotation on first Write.
func TestWALWriter_Write_TriggersRotation(t *testing.T) {
	dir := t.TempDir()

	var flushedCount int
	w, err := NewWALWriter(dir, func(cdrs []CDR) error {
		flushedCount += len(cdrs)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Make interval negative so rotation triggers immediately.
	w.mu.Lock()
	w.RotateInterval = -1
	w.mu.Unlock()

	// Write one CDR — should trigger rotation.
	if err := w.Write(makeCDR("rotate-trigger-1")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// After rotation a new file must be open.
	w.mu.Lock()
	hasFile := w.f != nil
	w.mu.Unlock()

	if !hasFile {
		t.Error("expected a new WAL file open after rotation-triggered write")
	}

	// At least one .wal file (the new current one) must exist in dir.
	entries, _ := os.ReadDir(dir)
	var walCount int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wal") {
			walCount++
		}
	}
	if walCount < 1 {
		t.Errorf("expected at least 1 WAL file after rotation, got %d", walCount)
	}

	// flushedCount may be 0 if rotation fired on a freshly-opened (empty) file;
	// the important thing is that the write succeeded and a new file is open.
	t.Logf("flushedCount after rotation-triggered write: %d", flushedCount)
}

// TestWALWriter_Rotate_OnFlushError verifies that when OnFlush returns an error
// during rotation, Write still succeeds (rotate swallows the OnFlush error via `_ = w.OnFlush(...)`).
func TestWALWriter_Rotate_OnFlushError(t *testing.T) {
	dir := t.TempDir()

	flushErr := errors.New("flush failed intentionally")
	w, err := NewWALWriter(dir, func(cdrs []CDR) error {
		return flushErr
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Write one CDR first so the WAL file has content for OnFlush to receive.
	if err := w.Write(makeCDR("pre-rotate")); err != nil {
		t.Fatalf("Write (pre-rotate): %v", err)
	}

	// Force rotation on next Write.
	w.mu.Lock()
	w.RotateInterval = -1
	w.mu.Unlock()

	// Write triggers rotation; rotate calls OnFlush which returns error.
	// rotate() does `_ = w.OnFlush(cdrs)` — the error is intentionally discarded.
	if err := w.Write(makeCDR("rotate-on-flush-error")); err != nil {
		t.Fatalf("Write should succeed even when OnFlush errors; got: %v", err)
	}
}

// TestWALWriter_RecoverAndFlush_OnFlushError verifies that when OnFlush fails
// during recovery, the WAL file is NOT removed (left for next attempt).
func TestWALWriter_RecoverAndFlush_OnFlushError(t *testing.T) {
	dir := t.TempDir()

	// Write a valid CDR JSON line into a stale .wal file directly.
	stalePath := filepath.Join(dir, "cdrs_20240101T000000.000000000Z_0001.wal")
	validLine := []byte(`{"SessionID":"stale-session","AccountID":"acct","Region":"us-east-1","NodeID":"node1","Features":1,"PulseMs":6000,"BilledUnits":2}` + "\n")
	if err := os.WriteFile(stalePath, validLine, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a WALWriter that will see the stale file during recovery.
	flushErr := errors.New("flush failed — leave file on disk")
	w, err := NewWALWriter(dir, func(cdrs []CDR) error {
		return flushErr
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.RecoverAndFlush(); err != nil {
		t.Fatalf("RecoverAndFlush returned unexpected error: %v", err)
	}

	// The stale file must still be present because OnFlush failed.
	if _, statErr := os.Stat(stalePath); os.IsNotExist(statErr) {
		t.Error("expected stale WAL file to remain on disk when OnFlush fails, but it was removed")
	}
}

// TestWALWriter_Close_Idempotent calls Close twice; the second call must not error.
func TestWALWriter_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()

	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close — w.f is nil, should return nil immediately.
	if err := w.Close(); err != nil {
		t.Fatalf("second Close (idempotent): %v", err)
	}
}

// TestReadWALFile_FileNotFound verifies that readWALFile returns an error
// when the path does not exist (unexported, tested from same package).
func TestReadWALFile_FileNotFound(t *testing.T) {
	_, err := readWALFile("/nonexistent/path/that/does/not/exist.wal")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

// TestReadWALFile_CorruptedLines writes one valid and one corrupted CDR JSON
// line and asserts that readWALFile returns exactly the 1 valid CDR.
func TestReadWALFile_CorruptedLines(t *testing.T) {
	f, err := os.CreateTemp("", "billing-wal-corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Valid JSON line.
	validLine := `{"SessionID":"good","AccountID":"acct","Region":"us-east-1","NodeID":"node1","Features":1,"PulseMs":6000,"BilledUnits":1}` + "\n"
	// Corrupted JSON line.
	corruptLine := "NOT_VALID_JSON\n"

	if _, err := fmt.Fprint(f, validLine+corruptLine); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cdrs, err := readWALFile(f.Name())
	if err != nil {
		t.Fatalf("readWALFile: %v", err)
	}
	if len(cdrs) != 1 {
		t.Errorf("expected 1 CDR (corrupt line skipped), got %d", len(cdrs))
	}
}

// TestWALWriter_RecoverAndFlush_SkipsCurrentFile verifies that RecoverAndFlush
// does NOT process (or remove) the currently open WAL file.
func TestWALWriter_RecoverAndFlush_SkipsCurrentFile(t *testing.T) {
	dir := t.TempDir()

	var flushedSessionIDs []string
	w, err := NewWALWriter(dir, func(cdrs []CDR) error {
		for _, c := range cdrs {
			flushedSessionIDs = append(flushedSessionIDs, c.SessionID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Write a CDR to the current file (it remains open and must not be recovered).
	if err := w.Write(makeCDR("active-session")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Grab the current file name before recovery.
	w.mu.Lock()
	currentName := ""
	if w.f != nil {
		currentName = filepath.Base(w.f.Name())
	}
	w.mu.Unlock()

	if err := w.RecoverAndFlush(); err != nil {
		t.Fatalf("RecoverAndFlush: %v", err)
	}

	// OnFlush must NOT have been called with the active session's CDR
	// (the current file is open for writing and must be skipped).
	for _, sid := range flushedSessionIDs {
		if sid == "active-session" {
			t.Errorf("RecoverAndFlush flushed the currently open WAL file (%s) — it should have been skipped", currentName)
		}
	}

	// The current file must still exist.
	w.mu.Lock()
	fileStillOpen := w.f != nil
	w.mu.Unlock()
	if !fileStillOpen {
		t.Error("current WAL file was closed unexpectedly by RecoverAndFlush")
	}
}
