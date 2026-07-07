package billing

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWALWriter_Rotate_OpenNewFailsAfterRotate exercises the rotate() error
// path where, after the old WAL file is successfully closed (and its removal
// is a no-op because it no longer exists), the subsequent openNew() call
// fails because the WAL directory itself has been removed out from under the
// writer. This covers the remaining branch in rotate() where the trailing
// "return w.openNew()" propagates a real error.
func TestWALWriter_Rotate_OpenNewFailsAfterRotate(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Remove the whole WAL directory (including the currently open file's
	// directory entry) out from under the writer. The open fd stays valid
	// for Close(), os.Remove(path) on the now-missing file returns a
	// NotExist error (ignored by rotate), but the trailing openNew() call
	// must fail because Dir no longer exists.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	// Force rotation on the next Write.
	w.mu.Lock()
	w.RotateInterval = -1
	w.mu.Unlock()

	err = w.Write(makeCDR("after-dir-removed"))
	if err == nil {
		t.Fatal("expected error when openNew fails after rotate because Dir was removed")
	}
	if !strings.Contains(err.Error(), "billing/wal: open") {
		t.Errorf("expected wrapped openNew error, got: %v", err)
	}
}

// TestWALWriter_RecoverAndFlush_SkipsUnreadableFile verifies the
// "corrupted file" branch in RecoverAndFlush where readWALFile itself
// returns an error (as opposed to just skipping malformed individual lines).
// An unreadable file (permission denied on open) triggers this path: the
// file must be left on disk and recovery must continue with other files.
func TestWALWriter_RecoverAndFlush_SkipsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission checks don't apply")
	}

	dir := t.TempDir()

	badPath := filepath.Join(dir, "cdrs_20240101T000000.000000000Z_0002.wal")
	if err := os.WriteFile(badPath, []byte(`{"SessionID":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(badPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(badPath, 0o644) // allow TempDir cleanup

	goodPath := filepath.Join(dir, "cdrs_20240101T000000.000000000Z_0003.wal")
	validLine := []byte(`{"SessionID":"good-session","AccountID":"acct","Region":"us-east-1","NodeID":"node1","Features":1,"PulseMs":6000,"BilledUnits":1}` + "\n")
	if err := os.WriteFile(goodPath, validLine, 0o644); err != nil {
		t.Fatal(err)
	}

	var flushed []string
	w, err := NewWALWriter(dir, func(cdrs []CDR) error {
		for _, c := range cdrs {
			flushed = append(flushed, c.SessionID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.RecoverAndFlush(); err != nil {
		t.Fatalf("RecoverAndFlush: %v", err)
	}

	if _, statErr := os.Stat(badPath); os.IsNotExist(statErr) {
		t.Error("expected unreadable WAL file to remain on disk (treated as corrupted/skipped), but it was removed")
	}

	found := false
	for _, s := range flushed {
		if s == "good-session" {
			found = true
		}
	}
	if !found {
		t.Error("expected good-session to be flushed during recovery despite the unreadable sibling file")
	}
	if _, statErr := os.Stat(goodPath); !os.IsNotExist(statErr) {
		t.Error("expected good WAL file to be removed after successful recovery")
	}
}

// TestWALWriter_Write_MarshalError exercises the json.Marshal error branch
// in Write: json.Marshal rejects NaN/Inf float values, so a CDR containing
// a NaN float32 field triggers the "billing/wal: marshal CDR" error path.
func TestWALWriter_Write_MarshalError(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	cdr := makeCDR("bad-marshal")
	cdr.AvgLatencyMs = float32(math.NaN())

	err = w.Write(cdr)
	if err == nil {
		t.Fatal("expected marshal error for NaN float field")
	}
	if !strings.Contains(err.Error(), "marshal CDR") {
		t.Errorf("expected wrapped marshal error, got: %v", err)
	}
}

// TestWALWriter_Write_UnderlyingWriteError exercises the f.Write error branch
// in Write by closing the underlying *os.File directly (bypassing w.Close),
// leaving w.f non-nil but pointing at a closed file descriptor.
func TestWALWriter_Write_UnderlyingWriteError(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	w.mu.Lock()
	_ = w.f.Close()
	w.mu.Unlock()

	err = w.Write(makeCDR("write-after-close"))
	if err == nil {
		t.Fatal("expected error writing to closed underlying WAL file")
	}
	if !strings.Contains(err.Error(), "billing/wal: write") {
		t.Errorf("expected wrapped write error, got: %v", err)
	}
}
