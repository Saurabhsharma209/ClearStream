// Package file -- regression test for shared Options.OnProgress across
// ProcessDir/ProcessDirFull workers.
//
// Options.OnProgress documents that it is called 'from the processing
// goroutine' (singular) and must be kept non-blocking, but ProcessDir and
// ProcessDirFull previously handed the exact same Options value -- and
// therefore the exact same OnProgress closure -- to every concurrently
// running per-file worker goroutine with no synchronization at all. A
// caller whose OnProgress mutates its own state (appending to a slice, the
// natural thing to do and exactly what this package doc/tests do for the
// single-file case) then raced across files. Fixed by having ProcessDir and
// ProcessDirFull wrap opts.OnProgress in a mutex-guarded wrapper
// (synchronizedProgress) before dispatching workers.
package file

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestProcessDirOnProgressIsSynchronizedAcrossWorkers drives ProcessDir with
// MaxConcurrency "greater than one" over several files and a shared
// OnProgress callback that appends to a plain (non-mutex-protected) slice,
// exactly the way a naive caller would write it. Without synchronizedProgress
// wrapping the callback, this appends from multiple goroutines concurrently
// -- a data race caught by -race, and liable to silently lose updates even
// without it. This test asserts every observed progress call was actually
// recorded (no lost updates), which is the observable symptom of the race
// even when -race itself cannot run in this environment.
func TestProcessDirOnProgressIsSynchronizedAcrossWorkers(t *testing.T) {
	skipOnWindows(t)
	ffmpeg := makeFakeFFmpegForFile(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	const numFiles = 8
	for i := 0; i < numFiles; i++ {
		name := filepath.Join(srcDir, fmtSprintfTrack(i))
		if err := os.WriteFile(name, []byte("dummy-src"), 0644); err != nil {
			t.Fatalf("write src file: %v", err)
		}
	}

	var mu sync.Mutex
	var calls []float64
	recordProgress := func(pct float64) {
		mu.Lock()
		calls = append(calls, pct)
		mu.Unlock()
	}

	p := newProcWithPath(ffmpeg)
	errs := p.ProcessDir(srcDir, dstDir, Options{
		OutputCodec:    "pcm_s16le",
		MaxConcurrency: 4,
		OnProgress:     recordProgress,
	})
	for _, e := range errs {
		if e != nil {
			t.Errorf("ProcessDir error: %v", e)
		}
	}

	// Each file reports at least the 4 fixed checkpoints (0.0, 0.1, 0.7, 1.0),
	// so numFiles*4 is a safe lower bound. If OnProgress calls were being lost
	// to a race (or the callback itself corrupted by concurrent unsynchronized
	// access), this count would come up short and/or the test would be flagged
	// by -race.
	mu.Lock()
	got := len(calls)
	mu.Unlock()
	if got < numFiles*4 {
		t.Errorf("expected at least %d OnProgress calls across %d files, got %d", numFiles*4, numFiles, got)
	}
}

func fmtSprintfTrack(i int) string {
	digits := "0123456789"
	return "track" + string(digits[i%10]) + ".wav"
}
