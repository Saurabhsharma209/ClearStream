// Package main -- subprocess re-exec tests for main(), runServer(), and
// runRTP().
//
// These three were the last 0%-covered functions in this package (35.1%
// overall as of 2026-08-04's daily build, flagged on both 07-31 and 08-04 as
// needing a dedicated pass). All three either call os.Exit directly on the
// invalid-input path (main) or block indefinitely on success -- an HTTP
// server accept loop (runServer) or a signal wait (runRTP) -- so none of
// them can be called in-process from a normal test the way runFile/runDir/
// runProbe are in main_test.go/main_runfile_test.go without exiting or
// hanging the whole `go test` run.
//
// Standard fix (the same one net/http, os/exec, etc. use for themselves):
// re-exec the test binary as a subprocess with a marker env var set, and let
// TestHelperProcess -- which no-ops unless that marker is present -- become
// main() for that child process. The parent then drives the child like a
// real CLI invocation: read its stdout for the expected startup line, send
// it a real SIGINT/SIGTERM, and assert on its exit code and shutdown output.
package main

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// helperProcessEnv is set on re-exec'd children so TestHelperProcess knows to
// actually run main() instead of being a no-op under the normal test binary.
const helperProcessEnv = "CLEARSTREAM_WANT_HELPER_PROCESS=1"

// TestHelperProcess is not a real test. Under a plain `go test` run it does
// nothing (GO_WANT_HELPER_PROCESS-style guard below is unset). The tests in
// this file re-exec the test binary with -test.run=TestHelperProcess and the
// marker env var set, plus "--" followed by the CLI args to simulate; when
// invoked that way, this becomes the child process's entire main().
func TestHelperProcess(t *testing.T) {
	if os.Getenv("CLEARSTREAM_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Args = append([]string{"clearstream"}, args...)
	main()
	os.Exit(0)
}

// startHelperProcess re-execs the current test binary in helper-process mode
// with cliArgs standing in for a real `clearstream <cliArgs...>` invocation,
// returning the running command with stdout/stderr piped for inspection.
func startHelperProcess(t *testing.T, cliArgs ...string) (cmd *exec.Cmd, stdout *linesBuf, stderr *linesBuf) {
	t.Helper()
	testArgs := append([]string{"-test.run=TestHelperProcess", "--"}, cliArgs...)
	cmd = exec.Command(os.Args[0], testArgs...)
	cmd.Env = append(os.Environ(), helperProcessEnv)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	stdout, stderr = newLinesBuf(), newLinesBuf()

	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	stdout.consume(stdoutPipe)
	stderr.consume(stderrPipe)
	return cmd, stdout, stderr
}

// linesBuf collects a growing subprocess output stream line-by-line behind a
// mutex so the parent test goroutine can poll it safely while a background
// goroutine keeps reading.
type linesBuf struct {
	mu   sync.Mutex
	text strings.Builder
}

func newLinesBuf() *linesBuf { return &linesBuf{} }

func (b *linesBuf) consume(r interface{ Read([]byte) (int, error) }) {
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			b.mu.Lock()
			b.text.WriteString(sc.Text())
			b.text.WriteByte('\n')
			b.mu.Unlock()
		}
	}()
}

func (b *linesBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

// waitForSubstring polls buf for want, failing the test if it doesn't show up
// within timeout. Used to confirm the child has reached a known point in its
// startup sequence before we act on it (e.g. sending a signal).
func waitForSubstring(t *testing.T, buf *linesBuf, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in output; got:\n%s", want, buf.String())
}

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal delivery (SIGINT/SIGTERM) not exercised on Windows")
	}
}

// TestMain_NoArgs_PrintsUsageAndExitsNonZero drives main() itself (0% covered
// previously) via the subprocess re-exec pattern: os.Args[1:] empty is the
// documented "print usage, exit 1" path.
func TestMain_NoArgs_PrintsUsageAndExitsNonZero(t *testing.T) {
	cmd, stdout, _ := startHelperProcess(t)
	err := cmd.Wait()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got err=%v", err)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Fatalf("expected usage text on stdout, got:\n%s", stdout.String())
	}
}

// TestMain_UnknownCommand_ExitsNonZero drives main()'s default switch branch.
func TestMain_UnknownCommand_ExitsNonZero(t *testing.T) {
	cmd, _, stderr := startHelperProcess(t, "bogus-command")
	err := cmd.Wait()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got err=%v", err)
	}
	if !strings.Contains(stderr.String(), "unknown command: bogus-command") {
		t.Fatalf("expected unknown-command message on stderr, got:\n%s", stderr.String())
	}
}

// TestRunServer_StartsAndShutsDownCleanlyOnSIGTERM drives runServer() (0%
// covered previously) through its real lifecycle: start listening, receive
// SIGTERM, run the graceful-shutdown branch, and exit 0. ":0"/passthrough
// keep it independent of any real port or model backend.
func TestRunServer_StartsAndShutsDownCleanlyOnSIGTERM(t *testing.T) {
	skipIfWindows(t)
	cmd, stdout, _ := startHelperProcess(t, "server", "-http", "127.0.0.1:0", "-model", "passthrough")
	waitForSubstring(t, stdout, "ClearStream HTTP server listening on", 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit after SIGTERM, got: %v (stdout:\n%s)", err, stdout.String())
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("runServer did not shut down within 5s of SIGTERM; stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Server stopped cleanly.") {
		t.Fatalf("expected clean-shutdown message, got:\n%s", stdout.String())
	}
}

// TestRunRTP_StartsAndShutsDownCleanlyOnSIGTERM drives runRTP() (0% covered
// previously) through its real lifecycle: start the session, receive
// SIGTERM, print final stats, and exit 0. Loopback addresses and the
// passthrough model keep it independent of any real call traffic.
func TestRunRTP_StartsAndShutsDownCleanlyOnSIGTERM(t *testing.T) {
	skipIfWindows(t)
	cmd, stdout, _ := startHelperProcess(t, "rtp",
		"-listen", "127.0.0.1:0",
		"-forward", "127.0.0.1:1",
		"-model", "passthrough")
	waitForSubstring(t, stdout, "Press Ctrl+C to stop.", 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit after SIGTERM, got: %v (stdout:\n%s)", err, stdout.String())
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("runRTP did not shut down within 5s of SIGTERM; stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Final stats:") {
		t.Fatalf("expected final-stats line, got:\n%s", stdout.String())
	}
}
