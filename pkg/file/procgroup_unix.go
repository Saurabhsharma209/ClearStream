//go:build !windows

package file

import (
	"os/exec"
	"syscall"
)

// setNewProcessGroup configures cmd to run as the leader of a new process
// group, so that killProcessGroup (below) can later terminate it and any
// subprocesses it spawns together, rather than leaving orphaned descendants
// running after only the direct child is killed.
//
// This matters because exec.CommandContext's built-in ctx-cancellation
// handling only calls Process.Kill() on the single immediate child process.
// If that child forks its own subprocess (observed with this package's test
// suite, whose fake ffmpeg is a shell script that forks a separate "sleep"
// process; conceivably possible for real FFmpeg builds too), killing just
// the parent leaves the forked subprocess running -- and since it inherited
// the same stdout/stderr pipe file descriptors, its continued existence
// keeps those pipes open and delays cancellation until it finishes on its
// own, rather than promptly.
func setNewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to cmd's entire process group. Must only
// be called after cmd.Start() has returned successfully, and only on a cmd
// that was previously passed to setNewProcessGroup (so that its process
// group ID equals its own PID, letting the negative-PID form of kill(2)
// target the whole group).
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
