package billing

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WALWriter appends CDR JSON lines to a write-ahead log file.
// Rotates every RotateInterval (default 10 min).
// On startup, call RecoverAndFlush() to re-push any unflushed CDRs.
// Thread-safe.
type WALWriter struct {
	Dir            string
	RotateInterval time.Duration
	OnFlush        func(cdrs []CDR) error // called with batch on rotation/recovery

	mu      sync.Mutex
	f       *os.File
	created time.Time
}

// NewWALWriter creates a WALWriter that writes to dir.
// onFlush is invoked with a batch of CDRs when a WAL file is rotated or
// recovered; it may be nil (CDRs are still written to disk, just not forwarded).
func NewWALWriter(dir string, onFlush func([]CDR) error) (*WALWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("billing/wal: mkdir %s: %w", dir, err)
	}
	w := &WALWriter{
		Dir:            dir,
		RotateInterval: 10 * time.Minute,
		OnFlush:        onFlush,
	}
	if err := w.openNew(); err != nil {
		return nil, err
	}
	return w, nil
}

// walFileName returns a unique timestamped WAL file name.
// Uses nanosecond precision plus a 4-digit random suffix to avoid
// collisions when multiple writers start within the same second.
func walFileName(t time.Time) string {
	return fmt.Sprintf("cdrs_%s_%04d.wal",
		t.UTC().Format("20060102T150405.000000000Z"),
		rand.Intn(10000), //nolint:gosec — non-crypto, collision avoidance only
	)
}

// openNew opens a fresh WAL file. Caller must hold mu.
func (w *WALWriter) openNew() error {
	now := time.Now()
	path := filepath.Join(w.Dir, walFileName(now))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("billing/wal: open %s: %w", path, err)
	}
	w.f = f
	w.created = now
	return nil
}

// Write appends a CDR as a JSON line to the current WAL file, rotating if needed.
func (w *WALWriter) Write(cdr CDR) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate if the interval has elapsed.
	if time.Since(w.created) >= w.RotateInterval {
		if err := w.rotate(); err != nil {
			return err
		}
	}

	line, err := json.Marshal(cdr)
	if err != nil {
		return fmt.Errorf("billing/wal: marshal CDR: %w", err)
	}
	line = append(line, '\n')
	if _, err := w.f.Write(line); err != nil {
		return fmt.Errorf("billing/wal: write: %w", err)
	}
	return nil
}

// rotate flushes the current WAL file (calling OnFlush) and opens a new one.
// Caller must hold mu.
func (w *WALWriter) rotate() error {
	if w.f == nil {
		return w.openNew()
	}
	path := w.f.Name()
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("billing/wal: close for rotate: %w", err)
	}
	w.f = nil

	if w.OnFlush != nil {
		cdrs, err := readWALFile(path)
		if err == nil && len(cdrs) > 0 {
			_ = w.OnFlush(cdrs)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("billing/wal: remove rotated file: %w", err)
	}

	return w.openNew()
}

// RecoverAndFlush scans Dir for *.wal files that are not the current open file,
// calls OnFlush with their CDRs, and removes them. Safe to call at startup
// before any Write calls (or concurrently — it acquires mu).
func (w *WALWriter) RecoverAndFlush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return fmt.Errorf("billing/wal: readdir %s: %w", w.Dir, err)
	}

	currentName := ""
	if w.f != nil {
		currentName = filepath.Base(w.f.Name())
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".wal") {
			continue
		}
		if name == currentName {
			continue
		}
		path := filepath.Join(w.Dir, name)
		cdrs, err := readWALFile(path)
		if err != nil {
			// Corrupted file — skip but don't block recovery of others.
			continue
		}
		if w.OnFlush != nil && len(cdrs) > 0 {
			if err := w.OnFlush(cdrs); err != nil {
				// OnFlush failed — leave file on disk for next attempt.
				continue
			}
		}
		_ = os.Remove(path)
	}
	return nil
}

// Close closes the current WAL file without rotating
// (does NOT call OnFlush — the file remains for recovery on next startup).
func (w *WALWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// readWALFile reads all CDR JSON lines from path.
func readWALFile(path string) ([]CDR, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cdrs []CDR
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var cdr CDR
		if err := json.Unmarshal(line, &cdr); err != nil {
			continue // skip malformed lines
		}
		cdrs = append(cdrs, cdr)
	}
	return cdrs, scanner.Err()
}
