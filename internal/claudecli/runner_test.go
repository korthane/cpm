package claudecli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(got, "args=plugin list --json") {
		t.Errorf("output %q missing args", got)
	}
}

func TestRealRunnerNoProfileDirLeavesEnvUntouched(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/ambient")
	stub := writeScript(t, "#!/bin/sh\n"+`echo "config=$CLAUDE_CONFIG_DIR"`+"\n")
	r := &realRunner{binary: stub}

	out, err := r.Run(t.Context(), "", "auth", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "config=/ambient") {
		t.Errorf("output %q should preserve ambient env", out)
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
	if runErr.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", runErr.ExitCode)
	}
	if !strings.Contains(runErr.Stderr, "kaboom") {
		t.Errorf("Stderr = %q, want it to contain %q", runErr.Stderr, "kaboom")
	}
	if !strings.Contains(runErr.Error(), "kaboom") {
		t.Errorf("Error() = %q, want it to include stderr", runErr.Error())
	}
}
