//go:build !unix

package claudecli

import "os/exec"

// setProcessGroup is a no-op where process groups are unavailable; the
// default context cancel plus WaitDelay still bound Run.
func setProcessGroup(cmd *exec.Cmd) {}
