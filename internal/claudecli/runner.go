// Package claudecli wraps the public `claude` CLI behind a Runner interface so
// that reads and mutations can be executed per profile and faked in tests.
package claudecli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes the claude CLI against a specific profile directory.
//
// profileDir sets CLAUDE_CONFIG_DIR for the invocation so the command targets
// that profile; an empty profileDir leaves the ambient environment untouched.
// Run returns the command's stdout. A non-zero exit is reported as a *RunError
// carrying the captured stderr and exit code.
type Runner interface {
	Run(ctx context.Context, profileDir string, args ...string) ([]byte, error)
}

// RunError describes a failed claude CLI invocation.
type RunError struct {
	Args     []string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *RunError) Error() string {
	msg := fmt.Sprintf("claude %s: %v", strings.Join(e.Args, " "), e.Err)
	// The message renders inside single-line table cells; collapse interior
	// newlines and whitespace runs so multi-line stderr cannot split a row.
	if s := strings.Join(strings.Fields(e.Stderr), " "); s != "" {
		msg += ": " + s
	}
	return msg
}

func (e *RunError) Unwrap() error { return e.Err }

// realRunner runs the real `claude` binary via os/exec.
type realRunner struct {
	// binary is the executable to run; defaults to "claude".
	binary string
}

// NewRunner returns a Runner backed by the real `claude` CLI on PATH.
func NewRunner() Runner {
	return &realRunner{binary: "claude"}
}

func (r *realRunner) Run(ctx context.Context, profileDir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	if profileDir != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+profileDir)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		runErr := &RunError{Args: args, Stderr: stderr.String(), Err: err}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			runErr.ExitCode = exitErr.ExitCode()
		}
		return stdout.Bytes(), runErr
	}
	return stdout.Bytes(), nil
}
