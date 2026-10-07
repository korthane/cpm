package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	latests := make([]claudecli.LatestVersions, len(perProfile))
	for i := range latests {
		latests[i] = latest
	}
	return modelWithLatests(t, latests, perProfile...)
}

// modelWithLatests is modelWithCells where profile i carries latests[i].
func modelWithLatests(t *testing.T, latests []claudecli.LatestVersions,
	perProfile ...claudecli.PluginData,
) Model {
	t.Helper()
	m := modelWithCells(t, &claudecli.FakeRunner{}, perProfile...)
	for i, data := range perProfile {
		loaded, _ := m.Update(profileLoadedMsg{index: i, plugins: data,
			latest: latests[i]})
		m = loaded.(Model)
	}
	return m
}

// renderedStatus is the status line as View draws it.
func renderedStatus(m Model) string {
	return m.statusLine(m.selectedChangeLink())
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
	openURL = func(url string) error {
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

	if got := renderedStatus(m); !strings.Contains(got, "changes: "+compareURL) {
		t.Errorf("statusLine() = %q, want changes: %s", got, compareURL)
	}
}

func TestStatusLineFallsBackToHistoryLink(t *testing.T) {
	m := outdatedFooModel(t, fooSource, "")

	if got := renderedStatus(m); !strings.Contains(got, "changes: "+historyURL) {
		t.Errorf("statusLine() = %q, want changes: %s", got, historyURL)
	}
}

func TestStatusLineChangeLinkIsWidthCapped(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})
	m = resized.(Model)

	got := renderedStatus(m)
	if len([]rune(got)) > 20 {
		t.Errorf("statusLine() = %q, longer than the 20-column terminal", got)
	}
	if !strings.HasPrefix(got, "changes: ") || !strings.HasSuffix(got, "…") {
		t.Errorf("statusLine() = %q, want a truncated changes: link", got)
	}
}

func TestStatusAndPromptTakePrecedenceOverChangeLink(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)
	m.setStatus("something happened", false)
	if got := renderedStatus(m); strings.Contains(got, "changes:") {
		t.Errorf("status pending, statusLine() = %q, want no link", got)
	}

	m.setStatus("", false)
	m, _ = press(t, m, "x")
	if m.pending == nil {
		t.Fatal("x did not arm the uninstall prompt")
	}
	if got := renderedStatus(m); strings.Contains(got, "changes:") {
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
			if got := renderedStatus(m); strings.Contains(got, "changes:") {
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
		{"up to date cell", func(t *testing.T) Model {
			m := outdatedFooModel(t, fooSource, installedSHA)
			m, _ = press(t, m, "right")
			return m
		}},
		{"outdated, non-GitHub repo", func(t *testing.T) Model {
			src := fooSource
			src.RepoURL = "https://git.example.com/acme/widgets"
			return outdatedFooModel(t, src, installedSHA)
		}},
		{"outdated, no source", func(t *testing.T) Model {
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

func TestOpenWithRefusesNonGitHubURLs(t *testing.T) {
	// A command that fails loudly proves the guard returns before exec.
	for _, url := range []string{
		"", "http://github.com/acme/widgets", "https://github.com.example.com/x",
		"https://example.com/", "file:///etc/passwd", "/Applications/Foo.app",
		"https://github.com", "-a Calculator",
	} {
		err := openWith("/nonexistent/opener", time.Second, url)
		if !errors.Is(err, errNotGitHubURL) {
			t.Errorf("openWith(%q) = %v, want errNotGitHubURL", url, err)
		}
	}
}

func TestOpenWithReportsOpenerExit(t *testing.T) {
	if err := openWith("true", time.Second, compareURL); err != nil {
		t.Errorf("succeeding opener = %v, want nil", err)
	}
	if err := openWith("false", time.Second, compareURL); err == nil {
		t.Error("failing opener = nil, want error")
	}
	if err := openWith("/nonexistent/opener", time.Second,
		compareURL); err == nil {
		t.Error("missing opener = nil, want error")
	}
}

// xdg-open may block until the browser exits; a still-running opener has
// handed the URL over, so it must not read as a failure.
func TestOpenWithTreatsLongRunningOpenerAsOpened(t *testing.T) {
	script := filepath.Join(t.TempDir(), "slow-opener")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"),
		0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := openWith(script, 50*time.Millisecond, compareURL)
	if err != nil {
		t.Errorf("long-running opener = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("openWith blocked %v on a long-running opener", elapsed)
	}
}

const (
	barSHA       = "2b3c4d5"
	barLatestSHA = "6e7f809"
	otherSHA     = "9a8b7c6"
)

var barID = claudecli.PluginID{Name: "bar", Marketplace: "mp"}

// twoOutdatedModel installs bar and foo, both behind their latest, in p0,
// and foo behind it in p1 from a different commit. Rows: mp, bar, foo.
func twoOutdatedModel(t *testing.T) Model {
	t.Helper()
	latest := claudecli.LatestVersions{
		Versions: map[claudecli.PluginID]string{
			fooID: "1.2.0", barID: "3.0.0"},
		Sources: map[claudecli.PluginID]claudecli.PluginSource{
			fooID: fooSource,
			barID: {RepoURL: "acme/widgets", Commit: barLatestSHA,
				Path: "plugins/bar"},
		},
	}
	p0 := claudecli.PluginData{Installed: []claudecli.InstalledPlugin{
		{ID: barID, Version: "2.0.0", Enabled: true, Scope: "user",
			CommitSHA: barSHA},
		{ID: fooID, Version: "1.0.0", Enabled: true, Scope: "user",
			CommitSHA: installedSHA},
	}}
	return modelWithLatest(t, latest, p0, installedFooAt("1.1.0", otherSHA))
}

func compareLink(from, to string) string {
	return "https://github.com/acme/widgets/compare/" + from + "..." + to
}

func TestChangeLinkFollowsSelectedCell(t *testing.T) {
	m := twoOutdatedModel(t)
	m, _ = press(t, m, "down")
	if got, want := m.selectedChangeLink(), compareLink(barSHA,
		barLatestSHA); got != want {
		t.Errorf("bar in p0: link = %q, want %q", got, want)
	}

	m, _ = press(t, m, "down")
	if got := m.selectedChangeLink(); got != compareURL {
		t.Errorf("foo in p0: link = %q, want %q", got, compareURL)
	}

	m, _ = press(t, m, "right")
	want := compareLink(otherSHA, latestSHA)
	if got := m.selectedChangeLink(); got != want {
		t.Errorf("foo in p1: link = %q, want %q", got, want)
	}
	opened := stubOpener(t, nil)
	pressOpen(t, m)
	if len(*opened) != 1 || (*opened)[0] != want {
		t.Errorf("opened %v, want [%s]", *opened, want)
	}
}

// The filter hides bar, so the second row is foo: the link and `o` must
// follow the visible row, not the unfiltered index.
func TestChangeLinkFollowsFilteredRow(t *testing.T) {
	m := twoOutdatedModel(t)
	m = typeKeys(t, m, "/", "f", "o", "o", "enter")
	m, _ = press(t, m, "down")

	if got := renderedStatus(m); !strings.Contains(got, compareURL) {
		t.Errorf("statusLine() = %q, want foo's link %s", got, compareURL)
	}
	opened := stubOpener(t, nil)
	pressOpen(t, m)
	if len(*opened) != 1 || (*opened)[0] != compareURL {
		t.Errorf("opened %v, want [%s]", *opened, compareURL)
	}
}

func TestNoChangeLinkOnFoldedHeader(t *testing.T) {
	m := twoOutdatedModel(t)
	m, _ = press(t, m, "enter")
	if !m.folded["mp"] {
		t.Fatal("enter did not fold mp")
	}
	m, _ = press(t, m, "down")

	if got := m.selectedChangeLink(); got != "" {
		t.Errorf("link = %q on a folded header, want none", got)
	}
	opened := stubOpener(t, nil)
	if _, cmd := press(t, m, "o"); cmd != nil || len(*opened) != 0 {
		t.Errorf("o on a folded header opened %v", *opened)
	}
}

// A column that failed to reload keeps stale data; its source must not
// supply the link even though its profile comes first.
func TestChangeLinkSkipsErroredColumnSource(t *testing.T) {
	stale := fooSource
	stale.Commit = "aaaaaaa"
	m := modelWithLatests(t,
		[]claudecli.LatestVersions{fooLatest(stale), fooLatest(fooSource)},
		installedFooAt("1.2.0", "aaaaaaa"), installedFooAt("1.0.0", installedSHA))
	errored, _ := m.Update(profileErrMsg{index: 0, err: errors.New("boom")})
	m = errored.(Model)
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "right")

	if got := m.selectedChangeLink(); got != compareURL {
		t.Errorf("link = %q, want %q from the loaded column", got, compareURL)
	}
}

// chromeLines budgets one row per help line; the longer action line with
// `o: open changes` must not soft-wrap on a narrow terminal.
func TestHelpLinesAreWidthCapped(t *testing.T) {
	m := outdatedFooModel(t, fooSource, installedSHA)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = resized.(Model)

	view := m.View()
	for _, prefix := range []string{"←/→", "e: enable"} {
		found := false
		for line := range strings.Lines(view) {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			found = true
			if w := len([]rune(strings.TrimSuffix(line, "\n"))); w > 60 {
				t.Errorf("help line %q is %d wide, want at most 60", line, w)
			}
		}
		if !found {
			t.Errorf("no help line starting %q in:\n%s", prefix, view)
		}
	}
}

// While a prompt is pending `o` cancels it, so the hint would lie and the
// opener must never run.
func TestOpenKeyDuringPromptCancelsInsteadOfOpening(t *testing.T) {
	opened := stubOpener(t, nil)
	m := outdatedFooModel(t, fooSource, installedSHA)
	m, _ = press(t, m, "x")
	if m.pending == nil {
		t.Fatal("x did not arm the uninstall prompt")
	}
	if strings.Contains(m.View(), "o: open changes") {
		t.Errorf("help advertises o during a prompt:\n%s", m.View())
	}

	m = pressOpen(t, m)
	if len(*opened) != 0 {
		t.Errorf("o during a prompt opened %v", *opened)
	}
	if m.pending != nil {
		t.Error("o left the prompt pending, want it answered as no")
	}
}
