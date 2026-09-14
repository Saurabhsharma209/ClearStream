//go:build !windows

package model

import (
	"os/exec"
	"testing"
	"time"
)

// TestKillProcessGroupNilCmd verifies killProcessGroup does not panic when
// handed a nil *exec.Cmd, e.g. a caller that never successfully started a
// server process.
func TestKillProcessGroupNilCmd(t *testing.T) {
	killProcessGroup(nil)
}

// TestKillProcessGroupNilProcess verifies killProcessGroup does not panic
// when handed a *exec.Cmd that was constructed but never started (so
// cmd.Process is nil), which is the shape callers hold before
// cmd.Start() returns successfully.
func TestKillProcessGroupNilProcess(t *testing.T) {
	cmd := exec.Command("sleep", "0")
	killProcessGroup(cmd)
}

// TestKillProcessGroupKillsRunningProcess is the end-to-end regression guard
// for killProcessGroup actually terminating a running process group: after
// setNewProcessGroup + Start(), killProcessGroup must kill the process so
// callers (e.g. DeepFilterNet server shutdown) do not leak subprocesses.
func TestKillProcessGroupKillsRunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	setNewProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	killProcessGroup(cmd)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("killProcessGroup did not terminate the process within 5s")
	}
}
