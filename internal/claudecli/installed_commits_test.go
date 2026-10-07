package claudecli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	widgetPath = "/home/u/.claude/plugins/cache/example-market/widget/1.2.0"
	widgetSHA  = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	helperPath = "/home/u/.claude/plugins/cache/acme-tools/helper/2.1.0"
	helperSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// writeInstalledPlugins writes content as <profileDir>/plugins/
// installed_plugins.json.
func writeInstalledPlugins(t *testing.T, profileDir, content string) {
	t.Helper()
	dir := filepath.Join(profileDir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "installed_plugins.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installed(id, scope, path string) InstalledPlugin {
	return InstalledPlugin{
		ID: ParsePluginID(id), Scope: scope, InstallPath: path,
	}
}

func TestFillInstalledCommitsMatchesInstallPath(t *testing.T) {
	t.Parallel()
	profile := t.TempDir()
	writeInstalledPlugins(t, profile,
		string(readFixture(t, "installed_plugins.json")))

	plugins := []InstalledPlugin{
		installed("widget@example-market", "user", widgetPath),
		// The entry exists but carries no gitCommitSha.
		installed("gadget@example-market", "user",
			"/home/u/.claude/plugins/cache/example-market/gadget/0.3.0"),
		// Two project-scope entries: only the path tells them apart.
		installed("helper@acme-tools", "project", helperPath),
		installed("absent@example-market", "user", "/nowhere"),
	}
	fillInstalledCommits(profile, plugins)

	want := []string{widgetSHA, "", helperSHA, ""}
	for i, p := range plugins {
		if p.CommitSHA != want[i] {
			t.Errorf("%s CommitSHA = %q, want %q", p.ID, p.CommitSHA, want[i])
		}
	}
}

func TestFillInstalledCommitsFallsBackToUniqueScope(t *testing.T) {
	t.Parallel()
	profile := t.TempDir()
	writeInstalledPlugins(t, profile,
		string(readFixture(t, "installed_plugins.json")))

	plugins := []InstalledPlugin{
		// One user-scope entry for the id: it matches despite the path.
		installed("widget@example-market", "user", "/elsewhere/widget"),
		installed("widget@example-market", "user", ""),
		// Two project-scope entries: ambiguous, so no SHA.
		installed("helper@acme-tools", "project", "/elsewhere/helper"),
		// No entry with this scope.
		installed("widget@example-market", "local", "/elsewhere/widget"),
	}
	fillInstalledCommits(profile, plugins)

	want := []string{widgetSHA, widgetSHA, "", ""}
	for i, p := range plugins {
		if p.CommitSHA != want[i] {
			t.Errorf("[%d] %s CommitSHA = %q, want %q",
				i, p.ID, p.CommitSHA, want[i])
		}
	}
}

func TestFillInstalledCommitsResolvesSymlinkedPaths(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realA := filepath.Join(base, "real", "helper-a")
	realB := filepath.Join(base, "real", "helper-b")
	for _, d := range []string{realA, realB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "real"), link); err != nil {
		t.Fatal(err)
	}

	profile := t.TempDir()
	writeInstalledPlugins(t, profile, `{"version": 2, "plugins": {
		"helper@acme-tools": [
			{"scope": "project", "gitCommitSha": "aaaa1111",
			 "installPath": `+strconv.Quote(filepath.Join(link, "helper-a"))+`},
			{"scope": "project", "gitCommitSha": "bbbb2222",
			 "installPath": `+strconv.Quote(filepath.Join(link, "helper-b")+"/")+`}
		]}}`)

	plugins := []InstalledPlugin{
		installed("helper@acme-tools", "project", realB),
		installed("helper@acme-tools", "project", realA),
	}
	fillInstalledCommits(profile, plugins)

	if plugins[0].CommitSHA != "bbbb2222" ||
		plugins[1].CommitSHA != "aaaa1111" {
		t.Errorf("CommitSHAs = %q, %q; want bbbb2222, aaaa1111",
			plugins[0].CommitSHA, plugins[1].CommitSHA)
	}
}

func TestFillInstalledCommitsIgnoresUnreadableFile(t *testing.T) {
	t.Parallel()
	oversize := `{"version": 2, "plugins": {}, "pad": "` +
		strings.Repeat("x", maxCatalogFileBytes) + `"}`
	tests := []struct {
		name  string
		setup func(t *testing.T, profile string)
	}{
		{"missing file", func(*testing.T, string) {}},
		{"malformed JSON", func(t *testing.T, profile string) {
			writeInstalledPlugins(t, profile, `{"version": 2, "plugins": [`)
		}},
		{"unknown version", func(t *testing.T, profile string) {
			writeInstalledPlugins(t, profile, `{"version": 3, "plugins": {
				"widget@example-market": [{"scope": "user",
					"installPath": "/w", "gitCommitSha": "abc1234"}]}}`)
		}},
		{"oversize file", func(t *testing.T, profile string) {
			writeInstalledPlugins(t, profile, oversize)
		}},
		{"not a regular file", func(t *testing.T, profile string) {
			path := filepath.Join(profile, "plugins", "installed_plugins.json")
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profile := t.TempDir()
			tt.setup(t, profile)
			plugins := []InstalledPlugin{
				installed("widget@example-market", "user", "/w"),
			}
			fillInstalledCommits(profile, plugins)
			if plugins[0].CommitSHA != "" {
				t.Errorf("CommitSHA = %q, want empty", plugins[0].CommitSHA)
			}
		})
	}
}

// An empty profile dir is the default profile, which claude keeps in
// ~/.claude. Setenv forbids t.Parallel.
func TestFillInstalledCommitsDefaultProfileUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeInstalledPlugins(t, filepath.Join(home, ".claude"),
		string(readFixture(t, "installed_plugins.json")))

	plugins := []InstalledPlugin{
		installed("widget@example-market", "user", widgetPath),
	}
	fillInstalledCommits("", plugins)

	if plugins[0].CommitSHA != widgetSHA {
		t.Errorf("CommitSHA = %q, want %q", plugins[0].CommitSHA, widgetSHA)
	}
}

func TestLoadPluginsCachedFillsCommitSHA(t *testing.T) {
	t.Parallel()
	profile := t.TempDir()
	writeInstalledPlugins(t, profile,
		string(readFixture(t, "installed_plugins.json")))
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [{"id": "widget@example-market",
					"version": "1.2.0", "enabled": true, "scope": "user",
					"installPath": ` + strconv.Quote(widgetPath) + `}],
				"available": []
			}`)},
			"plugin marketplace list --json": {Stdout: []byte(`[]`)},
		},
	}

	data, _, err := LoadPluginsCached(t.Context(), f, profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.Installed) != 1 {
		t.Fatalf("Installed = %+v, want one entry", data.Installed)
	}
	got := data.Installed[0]
	if got.InstallPath != widgetPath || got.CommitSHA != widgetSHA {
		t.Errorf("Installed[0] = %+v, want InstallPath %q and CommitSHA %q",
			got, widgetPath, widgetSHA)
	}
}

// fillOne runs fillInstalledCommits on a single install against the given
// records of widget@example-market.
func fillOne(t *testing.T, records string, p InstalledPlugin) string {
	t.Helper()
	profile := t.TempDir()
	writeInstalledPlugins(t, profile, `{"version": 2, "plugins": {
		"widget@example-market": [`+records+`]}}`)
	plugins := []InstalledPlugin{p}
	fillInstalledCommits(profile, plugins)
	return plugins[0].CommitSHA
}

func TestFillInstalledCommitsPathMatchIsScoped(t *testing.T) {
	t.Parallel()
	const (
		path        = "/cache/widget/1.2.0"
		userSHA     = "aaaa1111"
		projectSHA  = "bbbb2222"
		userRec     = `{"scope": "user", "installPath": "/cache/widget/1.2.0", "gitCommitSha": "aaaa1111"}`
		projectRec  = `{"scope": "project", "installPath": "/cache/widget/1.2.0", "gitCommitSha": "bbbb2222"}`
		bareUserRec = `{"scope": "user", "installPath": "/cache/widget/1.2.0"}`
	)
	tests := []struct {
		name    string
		records string
		scope   string
		want    string
	}{
		{"project, user first", userRec + "," + projectRec, "project", projectSHA},
		{"project, project first", projectRec + "," + userRec, "project", projectSHA},
		{"user, user first", userRec + "," + projectRec, "user", userSHA},
		{"user, project first", projectRec + "," + userRec, "user", userSHA},
		{"other scope only", userRec, "project", ""},
		{"SHA-less record first", bareUserRec + "," + userRec, "user", userSHA},
		{"SHA-less record last", userRec + "," + bareUserRec, "user", userSHA},
		{"agreeing duplicates", userRec + "," + userRec, "user", userSHA},
		{"conflicting duplicates", userRec + "," +
			`{"scope": "user", "installPath": "/cache/widget/1.2.0", "gitCommitSha": "cccc3333"}`,
			"user", ""},
		{"only SHA-less match", bareUserRec + "," +
			`{"scope": "user", "installPath": "/other", "gitCommitSha": "cccc3333"}`,
			"user", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fillOne(t, tt.records,
				installed("widget@example-market", tt.scope, path))
			if got != tt.want {
				t.Errorf("CommitSHA = %q, want %q", got, tt.want)
			}
		})
	}
}
