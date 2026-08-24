//go:build !windows

package model

import (
	"os/exec"
	"syscall"
)

// setNewProcessGroup configures cmd to run as the leader of a new process
// group, so that killProcessGroup (below) can later terminate it and any
// subprocesses it spawns together, rather than leaving orphaned descendants
// running after only the direct child is killed.
func setNewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to cmd's entire process group. Must only be
// called after cmd.Start() has returned successfully, and only on a cmd that
// was previously passed to setNewProcessGroup (so its process group ID
// equals its own PID, letting the negative-PID form of kill(2) target the
// whole group).
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
