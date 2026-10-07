// Package claudecli wraps the public `claude` CLI behind a Runner interface so
// that reads and mutations can be executed per profile and faked in tests.
package claudecli

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// CommandTimeout is the default budget for one profile's sequence of claude
// calls (a load or an action), shared by the TUI and the CLI commands.
const CommandTimeout = 2 * time.Minute

// Runner executes the claude CLI against a specific profile directory.
//
// profileDir sets CLAUDE_CONFIG_DIR for the invocation so the command targets
// that profile; an empty profileDir targets the default profile, stripping any
// ambient CLAUDE_CONFIG_DIR from the environment.
// Run returns the command's stdout. A non-zero exit is reported as a *RunError
// carrying the captured stdout and stderr.
type Runner interface {
	Run(ctx context.Context, profileDir string, args ...string) ([]byte, error)
}

// maxDetailRunes bounds the stdout excerpt in an error: a failed --json call
// can print a whole document there.
const maxDetailRunes = 300

// RunError describes a failed claude CLI invocation.
type RunError struct {
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

func (e *RunError) Error() string {
	msg := fmt.Sprintf("claude %s: %v", strings.Join(e.Args, " "), e.Err)
	// Some commands (e.g. `plugin marketplace update`) report failures on
	// stdout and leave stderr empty, so stdout is the fallback detail.
	detail := oneLine(e.Stderr)
	if detail == "" {
		detail = oneLine(e.Stdout)
		if r := []rune(detail); len(r) > maxDetailRunes {
			detail = string(r[:maxDetailRunes]) + "…"
		}
	}
	if detail != "" {
		msg += ": " + detail
	}
	return msg
}

// oneLine makes CLI output safe for single-line table cells: ANSI escapes
// are stripped (a sequence cut by cell truncation garbles the whole row) and
// newlines and whitespace runs collapse to single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(ansi.Strip(s)), " ")
}

func (e *RunError) Unwrap() error { return e.Err }

// realRunner runs the real `claude` binary via os/exec.
type realRunner struct {
	// binary is the executable to run; defaults to "claude".
	binary string
	// waitDelay overrides the post-kill wait bound (see Run); zero means the
	// 5s default. Only tests set it.
	waitDelay time.Duration
}

// NewRunner returns a Runner backed by the real `claude` CLI on PATH.
func NewRunner() Runner {
	return &realRunner{binary: "claude"}
}

func (r *realRunner) Run(ctx context.Context, profileDir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	// On timeout the default cancel kills only claude itself; its children
	// (stdio MCP servers spawned by `mcp list`, git spawned by `marketplace
	// update`) would survive as orphans and keep the stdout/stderr pipes open,
	// blocking cmd.Run past the very timeout the UI relies on. Kill the whole
	// process group instead (where the platform supports it), with WaitDelay
	// as the backstop that force-closes the pipes if anything still lingers.
	setProcessGroup(cmd)
	cmd.WaitDelay = cmp.Or(r.waitDelay, 5*time.Second)
	// An empty profileDir means "the default profile": strip any ambient
	// CLAUDE_CONFIG_DIR inherited from cpm's own environment, which would
	// otherwise silently redirect the call to another profile.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=")
	})
	if profileDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+profileDir)
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &RunError{
			Args: args, Stdout: stdout.String(), Stderr: stderr.String(), Err: err,
		}
	}
	return stdout.Bytes(), nil
}
