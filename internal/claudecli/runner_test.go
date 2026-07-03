package claudecli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFakeRunnerReturnsCannedOutputAndRecordsCalls(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin list --json": {Stdout: []byte("[]")},
		},
	}

	out, err := f.Run(t.Context(), "/home/u/.claude", "plugin", "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "[]" {
		t.Fatalf("stdout = %q, want %q", out, "[]")
	}

	if len(f.Calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(f.Calls))
	}
	call := f.Calls[0]
	if call.ProfileDir != "/home/u/.claude" {
		t.Errorf("ProfileDir = %q, want %q", call.ProfileDir, "/home/u/.claude")
	}
	if strings.Join(call.Args, " ") != "plugin list --json" {
		t.Errorf("Args = %v, want [plugin list --json]", call.Args)
	}
}

func TestFakeRunnerSurfacesCannedError(t *testing.T) {
	wantErr := errors.New("boom")
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"auth status --json": {Err: wantErr},
		},
	}

	_, err := f.Run(t.Context(), "", "auth", "status", "--json")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestFakeRunnerFallsBackToDefault(t *testing.T) {
	f := &FakeRunner{Default: FakeResponse{Stdout: []byte("default")}}

	out, err := f.Run(t.Context(), "", "anything", "goes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "default" {
		t.Fatalf("stdout = %q, want %q", out, "default")
	}
}

// writeScript creates an executable shell script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestRealRunnerSetsConfigDirAndArgs(t *testing.T) {
	// An ambient CLAUDE_CONFIG_DIR (cpm itself launched with one exported)
	// must lose to the profile's own dir, or every call would silently
	// mutate the ambient profile instead.
	t.Setenv("CLAUDE_CONFIG_DIR", "/ambient")
	// The stub echoes the profile env var and its args so we can assert the
	// realRunner wires both up correctly.
	stub := writeScript(t, "#!/bin/sh\n"+
		`echo "config=$CLAUDE_CONFIG_DIR"`+"\n"+
		`echo "args=$*"`+"\n")
	r := &realRunner{binary: stub}

	out, err := r.Run(t.Context(), "/tmp/profile-x", "plugin", "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "config=/tmp/profile-x") {
		t.Errorf("output %q missing config dir", got)
	}
	if strings.Contains(got, "/ambient") {
		t.Errorf("output %q leaked the ambient config dir", got)
	}
	if !strings.Contains(got, "args=plugin list --json") {
		t.Errorf("output %q missing args", got)
	}
}

func TestRealRunnerNoProfileDirStripsAmbientConfigDir(t *testing.T) {
	// An empty profileDir means "the default profile": an ambient
	// CLAUDE_CONFIG_DIR inherited from cpm's own environment would silently
	// redirect the call to another profile, so it must be stripped.
	t.Setenv("CLAUDE_CONFIG_DIR", "/ambient")
	t.Setenv("CPM_TEST_MARKER", "kept")
	stub := writeScript(t, "#!/bin/sh\n"+
		`echo "config=${CLAUDE_CONFIG_DIR-unset}"`+"\n"+
		`echo "marker=$CPM_TEST_MARKER"`+"\n")
	r := &realRunner{binary: stub}

	out, err := r.Run(t.Context(), "", "auth", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "config=unset") {
		t.Errorf("output %q should have CLAUDE_CONFIG_DIR stripped", out)
	}
	if !strings.Contains(string(out), "marker=kept") {
		t.Errorf("output %q should keep the rest of the environment", out)
	}
}

func TestRealRunnerSurfacesNonZeroExitAndStderr(t *testing.T) {
	stub := writeScript(t, "#!/bin/sh\n"+`echo "kaboom" >&2`+"\nexit 3\n")
	r := &realRunner{binary: stub}

	_, err := r.Run(t.Context(), "", "plugin", "list")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	runErr, ok := errors.AsType[*RunError](err)
	if !ok {
		t.Fatalf("err type = %T, want *RunError", err)
	}
	if !strings.Contains(runErr.Stderr, "kaboom") {
		t.Errorf("Stderr = %q, want it to contain %q", runErr.Stderr, "kaboom")
	}
	if !strings.Contains(runErr.Error(), "kaboom") {
		t.Errorf("Error() = %q, want it to include stderr", runErr.Error())
	}
	// The wrapper must stay unwrappable to the underlying exec error.
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Errorf("err = %v does not unwrap to *exec.ExitError", err)
	}
}

func TestRunErrorCollapsesMultiLineStderr(t *testing.T) {
	err := &RunError{
		Args:   []string{"plugin", "list"},
		Stderr: "Error: fetch failed\n  at https://example.com\n\nretry later\n",
		Err:    errors.New("exit status 1"),
	}

	msg := err.Error()
	// The message ends up in single-line table cells; an embedded newline
	// would split a rendered row.
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("Error() = %q, want no newlines", msg)
	}
	for _, want := range []string{"fetch failed", "retry later"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to contain %q", msg, want)
		}
	}
}

func TestRunErrorStripsANSISequences(t *testing.T) {
	err := &RunError{
		Args:   []string{"plugin", "list"},
		Stderr: "\x1b[31mError:\x1b[0m fetch \x1b[1mfailed\x1b[22m",
		Err:    errors.New("exit status 1"),
	}

	msg := err.Error()
	// The message renders in table cells whose width-aware truncation would
	// count and cut escape sequences wrongly, garbling the row.
	if strings.Contains(msg, "\x1b") {
		t.Errorf("Error() = %q, want ANSI escapes stripped", msg)
	}
	if !strings.Contains(msg, "Error: fetch failed") {
		t.Errorf("Error() = %q, want the plain stderr text kept", msg)
	}
}

func TestRealRunnerHonorsCancelledContext(t *testing.T) {
	stub := writeScript(t, "#!/bin/sh\nsleep 30\n")
	r := &realRunner{binary: stub}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := r.Run(ctx, "", "anything"); err == nil {
		t.Fatal("expected error from a cancelled context, got nil")
	}
}

func TestRealRunnerKillsHungProcess(t *testing.T) {
	// The UI relies on command timeouts to degrade a hung claude to a
	// per-column error; this pins the mid-execution kill, not just the
	// pre-cancelled fast path above.
	stub := writeScript(t, "#!/bin/sh\nsleep 30\n")
	r := &realRunner{binary: stub}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.Run(ctx, "", "anything")
	if err == nil {
		t.Fatal("expected error from a timed-out run, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("run took %v, the process was not killed on timeout", elapsed)
	}
}

func TestRealRunnerTimeoutSurvivesPipeHoldingGrandchild(t *testing.T) {
	// A grandchild that inherited the stdout pipe (stdio MCP server, git)
	// would keep Run blocked until it exits; the process-group kill (with
	// WaitDelay closing the pipes as backstop) must bound the return. The
	// ready file gates the kill: cancelling on a fixed timer can fire before
	// the stub has spawned the grandchild, which would pass trivially.
	ready := filepath.Join(t.TempDir(), "ready")
	stub := writeScript(t, "#!/bin/sh\nsleep 30 &\ntouch "+ready+"\nwait\n")
	r := &realRunner{binary: stub, waitDelay: 100 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(ready); err == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	start := time.Now()
	_, err := r.Run(ctx, "", "anything")
	if err == nil {
		t.Fatal("expected error from a killed run, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("run took %v, the orphaned pipe holder blocked Wait", elapsed)
	}
}
