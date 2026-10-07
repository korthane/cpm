package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
)

const (
	installedSHA = "1a2b3c4"
	latestSHA    = "5d6e7f8"
	compareURL   = "https://github.com/acme/widgets/compare/" +
		installedSHA + "..." + latestSHA
	historyURL = "https://github.com/acme/widgets/commits/" +
		latestSHA + "/plugins/foo"
)

// fooSource is where foo's latest version (1.2.0) lives in the fixtures.
var fooSource = claudecli.PluginSource{
	RepoURL: "acme/widgets", Commit: latestSHA, Path: "plugins/foo",
}

// installedFooAt is foo installed at version from commit sha.
func installedFooAt(version, sha string) claudecli.PluginData {
	return claudecli.PluginData{Installed: []claudecli.InstalledPlugin{{
		ID: fooID, Version: version, Enabled: true, Scope: "user",
		CommitSHA: sha,
	}}}
}

// fooLatest resolves foo's latest version to 1.2.0 at fooSource.
func fooLatest(src claudecli.PluginSource) claudecli.LatestVersions {
	return claudecli.LatestVersions{
		Versions: map[claudecli.PluginID]string{fooID: "1.2.0"},
		Sources:  map[claudecli.PluginID]claudecli.PluginSource{fooID: src},
	}
}

// modelWithLatest is modelWithCells whose profiles also carry latest.
func modelWithLatest(t *testing.T, latest claudecli.LatestVersions,
	perProfile ...claudecli.PluginData,
) Model {
	t.Helper()
	m := modelWithCells(t, &claudecli.FakeRunner{}, perProfile...)
	for i, data := range perProfile {
		loaded, _ := m.Update(profileLoadedMsg{index: i, plugins: data, latest: latest})
		m = loaded.(Model)
	}
	return m
}

// outdatedFooModel selects foo's row in p0, where it is behind 1.2.0.
func outdatedFooModel(t *testing.T, src claudecli.PluginSource, sha string) Model {
	t.Helper()
	m := modelWithLatest(t, fooLatest(src),
		installedFooAt("1.0.0", sha), installedFooAt("1.2.0", latestSHA))
	m, _ = press(t, m, "down")
	return m
}

// stubOpener records the URLs passed to openURL and answers with err.
func stubOpener(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	prev := openURL
	openURL = func(_ context.Context, url string) error {
		opened = append(opened, url)
		return err
	}
	t.Cleanup(func() { openURL = prev })
	return &opened
}

// pressOpen presses o and applies every message its command produces.
func pressOpen(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := press(t, m, "o")
	for _, msg := range drain(t, cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	return m
}

func TestStatusLineShowsChangeLinkForOutdatedCell(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)

	if got := m.statusLine(); !strings.Contains(got, "changes: "+compareURL) {
		t.Errorf("statusLine() = %q, want changes: %s", got, compareURL)
	}
}

func TestStatusLineFallsBackToHistoryLink(t *testing.T) {
	m := outdatedFooModel(t, fooSource, "")

	if got := m.statusLine(); !strings.Contains(got, "changes: "+historyURL) {
		t.Errorf("statusLine() = %q, want changes: %s", got, historyURL)
	}
}

func TestStatusLineChangeLinkIsWidthCapped(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})
	m = resized.(Model)

	if got := m.statusLine(); len([]rune(got)) > 20 {
		t.Errorf("statusLine() = %q, longer than the 20-column terminal", got)
	}
}

func TestStatusAndPromptTakePrecedenceOverChangeLink(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)
	m.setStatus("something happened", false)
	if got := m.statusLine(); strings.Contains(got, "changes:") {
		t.Errorf("status pending, statusLine() = %q, want no link", got)
	}

	m.setStatus("", false)
	m, _ = press(t, m, "x")
	if m.pending == nil {
		t.Fatal("x did not arm the uninstall prompt")
	}
	if got := m.statusLine(); strings.Contains(got, "changes:") {
		t.Errorf("prompt pending, statusLine() = %q, want no link", got)
	}
}

func TestNoChangeLinkWithoutOutdatedLinkedCell(t *testing.T) {
	tests := []struct {
		name string
		m    func(t *testing.T) Model
	}{
		{"up to date cell", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "right")
			return m
		}},
		{"non-GitHub repo", func(t *testing.T) Model {
			src := fooSource
			src.RepoURL = "https://git.example.com/acme/widgets"
			return outdatedFooModel(t, src, installedSHA)
		}},
		{"no source", func(t *testing.T) Model {
			return outdatedFooModel(t, claudecli.PluginSource{}, installedSHA)
		}},
		{"marketplace header", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "up")
			return m
		}},
		{"MCP tab", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "tab")
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.m(t)
			if got := m.statusLine(); strings.Contains(got, "changes:") {
				t.Errorf("statusLine() = %q, want no link", got)
			}
			if strings.Contains(m.View(), "o: open changes") {
				t.Errorf("help advertises o without a link:\n%s", m.View())
			}
		})
	}
}

func TestHelpAdvertisesOpenOnlyWithLink(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)
	if !strings.Contains(m.View(), "o: open changes") {
		t.Errorf("help lacks o: open changes on a linked cell:\n%s", m.View())
	}
}

func TestOpenKeyOpensChangeLink(t *testing.T) {
	tests := []struct {
		name, sha, want string
	}{
		{"compare", installedSHA, compareURL},
		{"history fallback", "", historyURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opened := stubOpener(t, nil)
			m := pressOpen(t, outdatedFooModel(t, fooSource, tt.sha))

			if len(*opened) != 1 || (*opened)[0] != tt.want {
				t.Errorf("opened %v, want [%s]", *opened, tt.want)
			}
			if m.statusErr {
				t.Errorf("status error after a clean open: %q", m.status)
			}
		})
	}
}

func TestOpenFailureSetsErrorStatus(t *testing.T) {
	stubOpener(t, errors.New("no browser"))
	m := pressOpen(t, outdatedFooModel(t, fooSource, installedSHA))

	if !m.statusErr || !strings.Contains(m.status, "no browser") {
		t.Errorf("status = %q (err %v), want the opener error", m.status, m.statusErr)
	}
}

func TestOpenKeyNoopWithoutLink(t *testing.T) {
	tests := []struct {
		name string
		m    func(t *testing.T) Model
	}{
		{"link-less cell", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "right")
			return m
		}},
		{"marketplace header", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "up")
			return m
		}},
		{"MCP tab", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "tab")
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opened := stubOpener(t, nil)
			m, cmd := press(t, tt.m(t), "o")
			if cmd != nil {
				t.Errorf("o returned a command on a link-less selection")
			}
			if len(*opened) != 0 || m.status != "" {
				t.Errorf("opened %v, status %q; want a no-op", *opened, m.status)
			}
		})
	}
}

func TestOpenKeyTypesLiteralWhileFiltering(t *testing.T) {
	opened := stubOpener(t, nil)
	m := outdatedFooModel(t, fooSource, installedSHA)
	m, _ = press(t, m, "/")
	m = pressOpen(t, m)

	if got := m.filterInput.Value(); got != "o" {
		t.Errorf("filter input = %q, want %q", got, "o")
	}
	if len(*opened) != 0 {
		t.Errorf("opened %v while typing a filter", *opened)
	}
}

func TestOpenInBrowserRefusesNonGitHubURLs(t *testing.T) {
	// A command that fails loudly proves the guard returns before exec.
	prev := openerCommand
	openerCommand = "/nonexistent/opener"
	t.Cleanup(func() { openerCommand = prev })

	for _, url := range []string{
		"", "http://github.com/acme/widgets", "https://github.com.example.com/x",
		"https://example.com/", "file:///etc/passwd", "/Applications/Foo.app",
		"https://github.com", "-a Calculator",
	} {
		err := openInBrowser(context.Background(), url)
		if !errors.Is(err, errNotGitHubURL) {
			t.Errorf("openInBrowser(%q) = %v, want errNotGitHubURL", url, err)
		}
	}
}

func TestOpenInBrowserRunsOpener(t *testing.T) {
	prev := openerCommand
	t.Cleanup(func() { openerCommand = prev })

	openerCommand = "true"
	if err := openInBrowser(context.Background(), compareURL); err != nil {
		t.Errorf("openInBrowser with a succeeding opener = %v", err)
	}
	openerCommand = "false"
	if err := openInBrowser(context.Background(), compareURL); err == nil {
		t.Error("openInBrowser with a failing opener = nil, want error")
	}
}
