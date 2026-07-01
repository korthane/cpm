//go:build unix

package claudecli

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the child in its own process group and replaces the
// default context cancel (which kills only the child) with a group-wide
// SIGKILL, so processes claude spawns (stdio MCP servers, git) die with it on
// timeout instead of leaking and holding the output pipes open.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Cancel only runs after Start succeeds, so Process is non-nil.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
