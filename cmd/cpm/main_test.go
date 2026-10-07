package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

func TestResolveProfilesFromArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p1 := t.TempDir()
	p2 := t.TempDir()

	profiles, err := resolveProfiles([]string{p1, p2})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Path != p1 || profiles[1].Path != p2 {
		t.Fatalf("profiles = %+v, want %s and %s", profiles, p1, p2)
	}
}

func TestResolveProfilesAbsoluteArgsSkipHomeResolution(t *testing.T) {
	t.Setenv("HOME", "") // simulates an environment where $HOME is unset
	p1 := t.TempDir()

	profiles, err := resolveProfiles([]string{p1})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Path != p1 {
		t.Fatalf("profiles = %+v, want %s", profiles, p1)
	}
}

func TestResolveProfilesAbsoluteArgDetectsDefault(t *testing.T) {
	// Home must be resolved even when no arg needs ~ expansion: the default
	// ~/.claude profile passed as an absolute path still needs IsDefault for
	// the Keychain auth fallback.
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	profiles, err := resolveProfiles([]string{dir})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 1 || !profiles[0].IsDefault {
		t.Fatalf("profiles = %+v, want a single default profile", profiles)
	}
}

func TestResolveProfilesTildeArgRequiresHome(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := resolveProfiles([]string{"~/profile"})
	if err == nil || !strings.Contains(err.Error(), "resolve home dir") {
		t.Fatalf("resolveProfiles error = %v, want a resolve home dir error", err)
	}
}

func TestResolveProfilesAutoDiscovers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	profiles, err := resolveProfiles(nil)
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Path != dir {
		t.Fatalf("profiles = %+v, want just %s", profiles, dir)
	}
}

func TestResolveProfilesErrorsWhenNoneFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := resolveProfiles(nil); err == nil {
		t.Fatal("resolveProfiles with empty home returned no error")
	}
}

func TestResolveProfilesRejectsFlagLikeArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := resolveProfiles([]string{"--bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want unknown flag error", err)
	}
}

func TestResolveProfilesRejectsMissingDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := resolveProfiles([]string{"/nonexistent/profile-dir"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v, want not-a-directory error", err)
	}
}

func TestResolveProfilesRejectsFileAsProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveProfiles([]string{file})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v, want not-a-directory error", err)
	}
}

func TestResolveProfilesRejectsMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "cpm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profiles: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	// A broken config must abort, not silently fall back to auto-discovery.
	if _, err := resolveProfiles(nil); err == nil {
		t.Fatal("resolveProfiles with malformed config returned no error")
	}
}

func TestResolveProfilesArgsIgnoreMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "cpm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profiles: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	// CLI args win the precedence, so the (ignored) broken config must not
	// block an explicit invocation.
	p := t.TempDir()
	profiles, err := resolveProfiles([]string{p})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Path != p {
		t.Fatalf("profiles = %+v, want just %s", profiles, p)
	}
}

// fakeLauncher records TUI starts instead of launching Bubble Tea.
type fakeLauncher struct {
	runner   *claudecli.FakeRunner
	tuiCalls [][]config.Profile
	tuiErr   error
}

func (f *fakeLauncher) launcher() launcher {
	return launcher{
		runner: f.runner,
		startTUI: func(_ claudecli.Runner, profiles []config.Profile) error {
			f.tuiCalls = append(f.tuiCalls, profiles)
			return f.tuiErr
		},
	}
}

func runCapture(t *testing.T, f *fakeLauncher, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := f.launcher().run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunRoutesCommandToCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	f := &fakeLauncher{runner: &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [{"id": "foo@acme", "version": "0.34.0",
					"scope": "user", "enabled": true}],
				"available": [{"pluginId": "foo@acme", "version": "0.35.1"}]
			}`)},
			"plugin marketplace list --json": {Stdout: []byte("[]")},
		},
	}}

	code, stdout, stderr := runCapture(t, f, "outdated", "--json", dir)
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q, want 0 and empty", code, stderr)
	}
	var got struct {
		Profiles []struct{ Path string } `json:"profiles"`
		Outdated []struct {
			Plugin, Latest string
		} `json:"outdated"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].Path != dir {
		t.Fatalf("profiles = %+v, want just %s", got.Profiles, dir)
	}
	if len(got.Outdated) != 1 || got.Outdated[0].Plugin != "foo@acme" ||
		got.Outdated[0].Latest != "0.35.1" {
		t.Fatalf("outdated = %+v, want foo@acme latest 0.35.1", got.Outdated)
	}
	for _, c := range f.runner.Calls {
		if c.ProfileDir != dir {
			t.Fatalf("call %v ran in %q, want %q", c.Args, c.ProfileDir, dir)
		}
	}
	if len(f.tuiCalls) != 0 {
		t.Fatal("command started the TUI")
	}
}

func TestRunCommandHelpPrintsCommandUsage(t *testing.T) {
	f := &fakeLauncher{runner: &claudecli.FakeRunner{}}

	code, stdout, stderr := runCapture(t, f, "outdated", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q, want 0 and empty", code, stderr)
	}
	if !strings.HasPrefix(stdout, "usage: cpm outdated") {
		t.Fatalf("stdout = %q, want the outdated usage", stdout)
	}
	if len(f.runner.Calls) != 0 || len(f.tuiCalls) != 0 {
		t.Fatal("help ran claude or started the TUI")
	}
}

func TestRunTopLevelHelpListsCommands(t *testing.T) {
	f := &fakeLauncher{runner: &claudecli.FakeRunner{}}

	code, stdout, _ := runCapture(t, f, "--help")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	for _, want := range []string{"cpm outdated", "cpm refresh", "./outdated"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("usage %q lacks %q", stdout, want)
		}
	}
	if len(f.tuiCalls) != 0 {
		t.Fatal("help started the TUI")
	}
}

func TestRunProfileDirStartsTUI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	f := &fakeLauncher{runner: &claudecli.FakeRunner{}}

	code, stdout, stderr := runCapture(t, f, dir)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q, want 0 and silent",
			code, stdout, stderr)
	}
	if len(f.tuiCalls) != 1 || len(f.tuiCalls[0]) != 1 ||
		f.tuiCalls[0][0].Path != dir {
		t.Fatalf("TUI calls = %+v, want one with %s", f.tuiCalls, dir)
	}
	if len(f.runner.Calls) != 0 {
		t.Fatalf("TUI path ran claude directly: %+v", f.runner.Calls)
	}
}

func TestRunTUIErrorExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := &fakeLauncher{runner: &claudecli.FakeRunner{},
		tuiErr: errors.New("no tty")}

	code, _, stderr := runCapture(t, f, t.TempDir())
	if code != 1 || stderr != "cpm: no tty\n" {
		t.Fatalf("code = %d, stderr = %q, want 1 and the TUI error", code, stderr)
	}
}

func TestRunUnknownTopLevelFlagErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := &fakeLauncher{runner: &claudecli.FakeRunner{}}

	code, _, stderr := runCapture(t, f, "--bogus")
	if code != 1 || !strings.Contains(stderr, `unknown flag "--bogus"`) {
		t.Fatalf("code = %d, stderr = %q, want 1 and unknown flag", code, stderr)
	}
	if len(f.tuiCalls) != 0 {
		t.Fatal("unknown flag started the TUI")
	}
}

const noProfilesErr = "cpm: no profiles found: pass directories as " +
	"arguments or configure ~/.config/cpm/config.yaml\n"

// Usage and profile-resolution errors happen before cli.Run, so they stay
// plain stderr text even under --json.
func TestRunCommandErrorsBeforeRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"unknown flag", []string{"outdated", "--bogus"}, 2,
			"cpm: outdated: unknown flag \"--bogus\"\n"},
		{"unknown flag json", []string{"outdated", "--json", "--bogus"}, 2,
			"cpm: outdated: unknown flag \"--bogus\"\n"},
		{"missing dir", []string{"outdated", "/nonexistent"}, 1,
			"cpm: profile /nonexistent is not a directory\n"},
		{"missing dir json", []string{"outdated", "--json", "/nonexistent"}, 1,
			"cpm: profile /nonexistent is not a directory\n"},
		{"no profiles", []string{"outdated"}, 1, noProfilesErr},
		{"no profiles refresh json", []string{"refresh", "--json"}, 1,
			noProfilesErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := &fakeLauncher{runner: &claudecli.FakeRunner{}}

			code, stdout, stderr := runCapture(t, f, tc.args...)
			if code != tc.code || stderr != tc.want || stdout != "" {
				t.Fatalf("code = %d, stdout = %q, stderr = %q; want %d, empty, %q",
					code, stdout, stderr, tc.code, tc.want)
			}
			if len(f.runner.Calls) != 0 || len(f.tuiCalls) != 0 {
				t.Fatal("a rejected command ran claude or started the TUI")
			}
		})
	}
}
