//go:build !windows

package file

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestKillProcessGroupKillsChildAndGrandchild exercises killProcessGroup (0%
// covered as of the 2026-08-26 daily build) end-to-end: start a real
// subprocess in its own process group via setNewProcessGroup, confirm it has
// forked a grandchild (mirroring the FFmpeg-forks-a-subprocess scenario
// documented on setNewProcessGroup), call killProcessGroup, and verify both
// the direct child and the grandchild are gone -- not just the direct child,
// which is all cmd.Process.Kill() alone would guarantee.
func TestKillProcessGroupKillsChildAndGrandchild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $! > "+pidFile+"; wait")
	setNewProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil && len(b) > 0 {
			if n, convErr := strconv.Atoi(strings.TrimSpace(string(b))); convErr == nil && n > 0 {
				childPID = n
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		cmd.Process.Kill()
		t.Fatal("grandchild pid never appeared")
	}

	killProcessGroup(cmd)

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("parent shell did not exit after killProcessGroup")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); err != nil {
			return // ESRCH: grandchild is gone.
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild pid %d still alive after killProcessGroup", childPID)
}

// TestKillProcessGroupNilSafe confirms the documented nil-guards (nil cmd,
// and a cmd whose Process was never started) are true no-ops rather than
// panicking -- callers in processor.go invoke killProcessGroup from defers
// on paths where Start() may not have succeeded.
func TestKillProcessGroupNilSafe(t *testing.T) {
	killProcessGroup(nil)
	killProcessGroup(&exec.Cmd{})
}
