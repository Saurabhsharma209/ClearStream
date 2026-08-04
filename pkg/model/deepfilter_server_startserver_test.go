package model

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// skipOnWindowsStartServer skips fake-python3 shell-script tests on Windows,
// mirroring the pattern used in pkg/file/processor_withffmpeg_test.go.
func skipOnWindowsStartServer(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-python3 shell script not supported on Windows")
	}
}

// freeTCPPort asks the OS for an ephemeral free TCP port by briefly
// listening on 127.0.0.1:0, then releasing it.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fakeHealthServerPy is a real Python program (executed by the machine's real
// python3, invoked from our fake "python3" shell-script stub) that serves a
// bare-bones /health endpoint returning 200 OK, on the port given via the
// FAKE_PY_PORT environment variable. It ignores the df_server.py-style
// script path argument entirely — the test only cares that startServer's
// polling loop eventually sees a 200 from /health.
const fakeHealthServerPy = `
import http.server
import os

port = int(os.environ["FAKE_PY_PORT"])


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, *args):
        pass


http.server.HTTPServer(("127.0.0.1", port), Handler).serve_forever()
`

// makeFakePython3 creates a fake "python3" executable in a fresh temp dir,
// following the same fake-external-binary-via-PATH pattern used in
// pkg/file/processor_withffmpeg_test.go for ffmpeg.
//
// startServer invokes exec.Command("python3", scriptPath); our fake ignores
// scriptPath entirely. If ready is true, the fake sleeps briefly (simulating
// model load time) and then shells out to the machine's real python3 to
// serve /health on the port named by the FAKE_PY_PORT env var (which
// startServer's exec.Command call inherits from the test process's
// environment). If ready is false, the fake just sleeps far longer than any
// test deadline and never serves /health, so callers can exercise the
// timeout path deterministically.
func makeFakePython3(t *testing.T, ready bool) (dir string) {
	t.Helper()
	skipOnWindowsStartServer(t)

	dir = t.TempDir()

	var script string
	if ready {
		pyPath := filepath.Join(dir, "fake_health_server.py")
		if err := os.WriteFile(pyPath, []byte(fakeHealthServerPy), 0644); err != nil {
			t.Fatalf("makeFakePython3: write fake_health_server.py: %v", err)
		}
		script = "#!/bin/sh\n" +
			"sleep 0.2\n" +
			"exec /usr/bin/python3 \"" + pyPath + "\"\n"
	} else {
		// Never serves /health; sleeps long enough to outlive any test's
		// (shortened) deadline so startServer's timeout path is exercised
		// against a real, still-running, never-ready subprocess.
		script = "#!/bin/sh\nsleep 60\n"
	}

	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0755); err != nil {
		t.Fatalf("makeFakePython3: write python3: %v", err)
	}
	return dir
}

// putFakePython3OnPath prepends dir to PATH for the duration of the test so
// that exec.Command("python3", ...) inside startServer resolves to our fake
// executable instead of (or in addition to) any real python3 on the system.
func putFakePython3OnPath(t *testing.T, dir string) {
	t.Helper()
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
}

// dummyScriptPath returns a path to a real (but otherwise irrelevant) file
// on disk so startServer's os.Stat(scriptPath) check succeeds. The fake
// python3 stub ignores the argument's contents entirely.
func dummyScriptPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "df_server.py")
	if err := os.WriteFile(p, []byte("# fake df_server.py placeholder\n"), 0644); err != nil {
		t.Fatalf("dummyScriptPath: %v", err)
	}
	return p
}

// TestStartServer_Success exercises the happy path: python3 (faked) starts,
// startServer polls /health until it responds 200, and returns nil with
// s.cmd populated (proof the subprocess handle was retained for later
// Close()).
func TestStartServer_Success(t *testing.T) {
	fakeDir := makeFakePython3(t, true /* ready */)
	putFakePython3OnPath(t, fakeDir)

	port := freeTCPPort(t)
	t.Setenv("FAKE_PY_PORT", strconv.Itoa(port))

	s := &deepFilterServerSuppressor{
		serverURL: "http://127.0.0.1:" + strconv.Itoa(port),
		client:    &http.Client{Timeout: 2 * time.Second},
		logger:    makeTestLogger(),
		// Shrink the poll loop drastically so the test doesn't take
		// anywhere near the production 30s deadline. The fake server
		// becomes ready after ~0.2s, well within this budget.
		startupTimeout:      5 * time.Second,
		startupPollInterval: 50 * time.Millisecond,
	}
	defer s.Close()

	if err := s.startServer(dummyScriptPath(t)); err != nil {
		t.Fatalf("startServer: unexpected error: %v", err)
	}
	if s.cmd == nil {
		t.Fatal("startServer: expected s.cmd to be set after successful auto-start, got nil")
	}
	if s.cmd.Process == nil {
		t.Fatal("startServer: expected s.cmd.Process to be non-nil after Start()")
	}

	// The server should now actually answer /health via the normal ping path.
	if err := s.ping(); err != nil {
		t.Errorf("ping() after successful startServer: %v", err)
	}
}

// TestStartServer_ScriptNotFound verifies the os.Stat error branch: a
// nonexistent scriptPath must fail fast without ever invoking python3.
func TestStartServer_ScriptNotFound(t *testing.T) {
	s := &deepFilterServerSuppressor{
		serverURL: "http://127.0.0.1:1", // unreachable; must not matter, we fail before dialing out
		client:    &http.Client{Timeout: 1 * time.Second},
		logger:    makeTestLogger(),
	}

	err := s.startServer(filepath.Join(t.TempDir(), "does-not-exist.py"))
	if err == nil {
		t.Fatal("startServer: expected error for missing script, got nil")
	}
	if s.cmd != nil {
		t.Error("startServer: s.cmd should remain nil when the script is never started")
	}
}

// TestStartServer_RelativePathResolved verifies startServer resolves a
// relative scriptPath to an absolute one before stat-ing it (the
// filepath.Abs branch), by chdir-ing into the directory that holds the
// script and passing just the file's base name.
func TestStartServer_RelativePathResolved(t *testing.T) {
	fakeDir := makeFakePython3(t, true /* ready */)
	putFakePython3OnPath(t, fakeDir)

	port := freeTCPPort(t)
	t.Setenv("FAKE_PY_PORT", strconv.Itoa(port))

	scriptDir := t.TempDir()
	scriptName := "df_server.py"
	if err := os.WriteFile(filepath.Join(scriptDir, scriptName), []byte("# placeholder\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(scriptDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer os.Chdir(origWD)

	s := &deepFilterServerSuppressor{
		serverURL:           "http://127.0.0.1:" + strconv.Itoa(port),
		client:              &http.Client{Timeout: 2 * time.Second},
		logger:              makeTestLogger(),
		startupTimeout:      5 * time.Second,
		startupPollInterval: 50 * time.Millisecond,
	}
	defer s.Close()

	if err := s.startServer(scriptName); err != nil {
		t.Fatalf("startServer with relative path: unexpected error: %v", err)
	}
}

// TestStartServer_Timeout exercises the deadline-exceeded branch: python3
// starts successfully (Start() succeeds, s.cmd is set) but never serves
// /health, so startServer must give up, kill the subprocess, and return the
// "server did not become ready" error.
//
// startupTimeout/startupPollInterval are overridden to a few hundred
// milliseconds so this test proves the real polling-loop-then-kill behavior
// without waiting anywhere near the production 30-second deadline.
func TestStartServer_Timeout(t *testing.T) {
	fakeDir := makeFakePython3(t, false /* never ready */)
	putFakePython3OnPath(t, fakeDir)

	s := &deepFilterServerSuppressor{
		serverURL: "http://127.0.0.1:" + strconv.Itoa(freeTCPPort(t)),
		client:    &http.Client{Timeout: 100 * time.Millisecond},
		logger:    makeTestLogger(),
		// Deliberately tiny so the test runs in well under a second, while
		// still exercising the exact same deadline/poll/kill logic used in
		// production with the default 30s/500ms values.
		startupTimeout:      300 * time.Millisecond,
		startupPollInterval: 50 * time.Millisecond,
	}

	start := time.Now()
	err := s.startServer(dummyScriptPath(t))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("startServer: expected timeout error, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("startServer: took %s, expected it to respect the shortened startupTimeout", elapsed)
	}
	if s.cmd == nil {
		t.Fatal("startServer: expected s.cmd to be set (subprocess was started) even though it timed out")
	}

	// Regression guard: startServer must Wait() on the process it just killed,
	// not just Kill() it, otherwise the killed subprocess is left as a zombie
	// with no other code path to reap it (newDeepFilterServerSuppressor drops
	// the whole suppressor -- including s.cmd -- on this exact error path).
	// A non-nil ProcessState is os/exec's proof that Wait() completed.
	if s.cmd.ProcessState == nil {
		t.Error("startServer: expected s.cmd.ProcessState to be set after the timeout/kill path, indicating Wait() reaped the process (avoiding a zombie); got nil")
	}
}

// makeFakePython3CrashImmediately creates a fake "python3" that exits
// immediately with a non-zero status, simulating a startup crash (e.g. a
// missing Python dependency or an uncaught exception during model load).
func makeFakePython3CrashImmediately(t *testing.T) (dir string) {
	t.Helper()
	skipOnWindowsStartServer(t)
	dir = t.TempDir()
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0755); err != nil {
		t.Fatalf("makeFakePython3CrashImmediately: %v", err)
	}
	return dir
}

// TestStartServer_ProcessExitsEarly is a regression test for a startup
// crash (missing dependency, uncaught exception, etc.) that used to be
// invisible to startServer until the full startupTimeout elapsed: it kept
// polling /health against an already-dead process instead of noticing the
// exit immediately. startServer must now return promptly, well before the
// (deliberately generous, relative to the crash) timeout.
func TestStartServer_ProcessExitsEarly(t *testing.T) {
	fakeDir := makeFakePython3CrashImmediately(t)
	putFakePython3OnPath(t, fakeDir)

	s := &deepFilterServerSuppressor{
		serverURL: "http://127.0.0.1:" + strconv.Itoa(freeTCPPort(t)),
		client:    &http.Client{Timeout: 100 * time.Millisecond},
		logger:    makeTestLogger(),
		// Deliberately much longer than the crash should take to surface, so
		// a pass here proves fast failure rather than just being lucky with
		// a short timeout racing a slow poll.
		startupTimeout:      3 * time.Second,
		startupPollInterval: 50 * time.Millisecond,
	}
	start := time.Now()
	err := s.startServer(dummyScriptPath(t))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("startServer: expected an error when the subprocess exits immediately, got nil")
	}
	if elapsed >= 3*time.Second {
		t.Errorf("startServer: took %s, expected early-exit detection well under the 3s startupTimeout", elapsed)
	}
	if s.cmd.ProcessState == nil {
		t.Error("startServer: expected s.cmd.ProcessState to be set once the crashed process is reaped, got nil")
	}
}
