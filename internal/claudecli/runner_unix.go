//go:build unix

package claudecli

import (
	"errors"
	"os"
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
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			// The group exited on its own between the deadline firing and the
			// kill. Report ErrProcessDone (as the default cancel does): any
			// other error from Cancel makes Wait fail a run that may have
			// completed successfully at the exact timeout boundary.
			return os.ErrProcessDone
		}
		return err
	}
}
