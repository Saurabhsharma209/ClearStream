// Package main -- additional CLI subcommand coverage.
//
// Before this file, `go test -cover` on this package reported only 15.5% of
// statements covered: main_test.go exercised `runDir` (the `dir` subcommand)
// end-to-end, but `runFile` (the `file` subcommand -- the CLI's primary,
// most-documented use case per the package doc comment's own first example),
// `runProbe` (the `probe` subcommand), and `printUsage` all sat at 0%. A
// regression in any of them (e.g. the exact class of nil-Suppressor bug
// main_test.go's own doc comment describes having hit `runDir`) would not
// have been caught by the test suite. This file closes that gap for the
// three happy-path, non-os.Exit code paths using the same fake-ffmpeg
// pattern makeFakeFFmpegPair already established in main_test.go.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunFile_ProcessesSuccessfully drives the real `file` subcommand code
// path end-to-end (the exact function invoked by `clearstream file -i ... -o
// ...`) and verifies it produces the enhanced output file, using an
// explicit -ffmpeg flag pointed at the fake ffmpeg pair so the test has no
// dependency on a real ffmpeg being installed or on PATH resolution order.
func TestRunFile_ProcessesSuccessfully(t *testing.T) {
	fakeDir := makeFakeFFmpegPair(t)

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(srcDir, "call.wav")
	dst := filepath.Join(dstDir, "clean.wav")
	if err := os.WriteFile(src, []byte("dummy-src-bytes"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	runFile([]string{
		"-i", src,
		"-o", dst,
		"-model", "passthrough",
		"-ffmpeg", filepath.Join(fakeDir, "ffmpeg"),
	})

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected enhanced output at %s, got: %v", dst, err)
	}
}

// TestRunProbe_PrintsFileInfo drives the real `probe` subcommand code path
// end-to-end. runProbe hard-codes the ffmpeg binary name "ffmpeg" (unlike
// runFile/runDir, it has no -ffmpeg flag), so the fake ffmpeg pair is
// prepended onto PATH rather than passed as a flag.
func TestRunProbe_PrintsFileInfo(t *testing.T) {
	fakeDir := makeFakeFFmpegPair(t)
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "call.wav")
	if err := os.WriteFile(src, []byte("dummy-src-bytes"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	stdout := captureStdout(t, func() {
		runProbe([]string{src})
	})

	for _, want := range []string{"File:", "Container:", "Audio codec:", "Sample rate:", "Channels:", "Duration:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("runProbe output missing %q; full output:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, src) {
		t.Errorf("runProbe output missing the probed file path %q; full output:\n%s", src, stdout)
	}
}

// TestPrintUsage_ContainsExpectedCommands verifies printUsage (invoked by
// main() whenever no/an unknown subcommand is given) lists every real
// subcommand, so the help text can't silently drift out of sync with the
// switch statement in main() that dispatches them.
func TestPrintUsage_ContainsExpectedCommands(t *testing.T) {
	stdout := captureStdout(t, printUsage)

	for _, cmd := range []string{"file", "dir", "rtp", "server", "probe", "version"} {
		if !strings.Contains(stdout, cmd) {
			t.Errorf("printUsage output missing subcommand %q; full output:\n%s", cmd, stdout)
		}
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. fn must not run concurrently with other tests
// that also touch os.Stdout (none in this package do).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return buf.String()
}
