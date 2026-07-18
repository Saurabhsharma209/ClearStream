package billing

import (
	"os"
	"strings"
	"testing"
)

// TestWALWriter_Rotate_NilFileOpensNew exercises rotate()'s first branch —
// when w.f is nil (e.g. after Close(), or before any file has ever been
// opened), rotate() must skip straight to openNew() instead of dereferencing
// a nil *os.File (which would panic in Name()/Close()).
func TestWALWriter_Rotate_NilFileOpensNew(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Manually close and nil out the current file, mimicking the state left
	// behind by Close() — without going through WALWriter.Close() itself, so
	// we can call the unexported rotate() directly afterward.
	w.mu.Lock()
	if err := w.f.Close(); err != nil {
		w.mu.Unlock()
		t.Fatalf("closing underlying file: %v", err)
	}
	w.f = nil
	err = w.rotate()
	hasFile := w.f != nil
	w.mu.Unlock()

	if err != nil {
		t.Fatalf("rotate() with nil w.f: unexpected error: %v", err)
	}
	if !hasFile {
		t.Error("expected rotate() to open a new file when w.f was nil")
	}
}

// TestWALWriter_Rotate_CloseError exercises rotate()'s error path when
// closing the current WAL file fails. We close the underlying *os.File out
// from under the writer (without clearing w.f, unlike WALWriter.Close()),
// so rotate()'s own w.f.Close() call hits an already-closed file descriptor
// and returns a wrapped error instead of proceeding.
func TestWALWriter_Rotate_CloseError(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.mu.Lock()
	if err := w.f.Close(); err != nil {
		w.mu.Unlock()
		t.Fatalf("pre-closing underlying file: %v", err)
	}
	// Deliberately leave w.f non-nil and pointing at the now-closed fd, so
	// rotate() itself attempts (and fails) the close.
	err = w.rotate()
	w.mu.Unlock()

	if err == nil {
		t.Fatal("expected error from rotate() when closing an already-closed WAL file")
	}
	if !strings.Contains(err.Error(), "close for rotate") {
		t.Errorf("expected wrapped 'close for rotate' error, got: %v", err)
	}
}

// TestWALWriter_Rotate_RemoveError exercises rotate()'s final error branch —
// os.Remove(path) failing with something other than "not exist" (here,
// permission denied because the containing directory has had its write bit
// stripped). rotate() must propagate this as a wrapped error rather than
// silently proceeding to openNew().
func TestWALWriter_Rotate_RemoveError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission checks don't apply")
	}
	dir := t.TempDir()
	w, err := NewWALWriter(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Restore write permission so t.TempDir()'s own cleanup can succeed.
		_ = os.Chmod(dir, 0o755)
		w.Close()
	}()

	// Strip write permission on the directory so os.Remove(path) fails with
	// a permission error (not ErrNotExist) below.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	w.mu.Lock()
	err = w.rotate()
	w.mu.Unlock()

	if err == nil {
		t.Fatal("expected error from rotate() when os.Remove fails on a read-only directory")
	}
	if !strings.Contains(err.Error(), "remove rotated file") {
		t.Errorf("expected wrapped 'remove rotated file' error, got: %v", err)
	}
}
