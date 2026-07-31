//go:build windows

package file

import "os/exec"

// setNewProcessGroup is a no-op on Windows: process groups work differently
// there (job objects, not POSIX process groups), and none of this package's
// current Windows-targeted usage forks the kind of shell-wrapped subprocess
// that motivates the Unix implementation. exec.CommandContext's default
// single-process kill is relied on instead.
func setNewProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup falls back to killing just the direct child process on
// Windows. exec.CommandContext's ctx-cancellation already does this on
// cancel, so this is a harmless best-effort duplicate rather than a gap.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
