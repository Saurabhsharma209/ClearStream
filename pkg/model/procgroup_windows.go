//go:build windows

package model

import "os/exec"

// setNewProcessGroup is a no-op on Windows: process groups work differently
// there (job objects, not POSIX process groups). cmd.Process.Kill() (the
// existing single-process kill) is relied on instead.
func setNewProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup falls back to killing just the direct child process on
// Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
