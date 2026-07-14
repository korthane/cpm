package ui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
)

// quits reports whether cmd produces a quit message.
func quits(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	for _, msg := range drain(t, cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

// typeKeys presses each key of s in turn.
func typeKeys(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		m, _ = press(t, m, k)
	}
	return m
}

// multiPlugins spreads three plugins over two marketplaces, so a filter can
// narrow rows both within a group (mp: alpha, beta) and across groups.
func multiPlugins() claudecli.PluginData {
	plugin := func(name, mkt string) claudecli.InstalledPlugin {
		return claudecli.InstalledPlugin{
			ID:      claudecli.PluginID{Name: name, Marketplace: mkt},
			Version: "1.0.0",
			Enabled: true,
		}
	}
	return claudecli.PluginData{Installed: []claudecli.InstalledPlugin{
		plugin("alpha", "mp"), plugin("beta", "mp"), plugin("gamma", "other"),
	}}
}

func TestSlashOpensFilterInput(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")

	if !m.filterEditing {
		t.Fatal("filterEditing = false after /, want true")
	}
	if !strings.Contains(m.View(), "filter: ") {
		t.Errorf("View() has no filter prompt:\n%s", m.View())
	}
}

func TestFilterInputTakesRunes(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "f", "o", "o")

	if got := m.filters[tabPlugins]; got != "foo" {
		t.Errorf("filters[tabPlugins] = %q, want %q", got, "foo")
	}
	if !strings.Contains(m.View(), "filter: foo") {
		t.Errorf("View() does not show the typed query:\n%s", m.View())
	}
}

func TestFilterInputSwallowsQuitKey(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m, cmd := press(t, m, "q")

	if quits(t, cmd) {
		t.Error("q while filtering quit the app, want it typed into the input")
	}
	if got := m.filters[tabPlugins]; got != "q" {
		t.Errorf("filters[tabPlugins] = %q, want %q", got, "q")
	}
}

func TestFilterInputSwallowsActionKeys(t *testing.T) {
	runner := okRunner()
	m := modelWithCells(t, runner, installedFoo(true))

	m, _ = press(t, m, "/")
	m, _ = press(t, m, "d")

	if calls := pluginCalls(runner, "disable"); len(calls) != 0 {
		t.Errorf("d while filtering fired %v, want no action", calls)
	}
	if got := m.filters[tabPlugins]; got != "d" {
		t.Errorf("filters[tabPlugins] = %q, want %q", got, "d")
	}
}

func TestFilterInputCtrlCStillQuits(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	_, cmd := press(t, m, "ctrl+c")

	if !quits(t, cmd) {
		t.Error("ctrl+c while filtering did not quit")
	}
}

func TestFilterEnterKeepsQuery(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "f", "o")
	m, _ = press(t, m, "enter")

	if m.filterEditing {
		t.Error("filterEditing = true after enter, want the input closed")
	}
	if got := m.filters[tabPlugins]; got != "fo" {
		t.Errorf("filters[tabPlugins] = %q after enter, want %q", got, "fo")
	}
}

func TestFilterEscClearsQuery(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "f", "o")
	m, _ = press(t, m, "esc")

	if m.filterEditing {
		t.Error("filterEditing = true after esc, want the input closed")
	}
	if got := m.filters[tabPlugins]; got != "" {
		t.Errorf("filters[tabPlugins] = %q after esc, want it cleared", got)
	}
}

func TestEscClearsAppliedFilterWithInputClosed(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "f", "o")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "esc")

	if got := m.filters[tabPlugins]; got != "" {
		t.Errorf("filters[tabPlugins] = %q after esc, want it cleared", got)
	}
}

func TestSlashReopensInputPrefilled(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "f", "o")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "/")

	if !m.filterEditing {
		t.Fatal("filterEditing = false after re-opening with /")
	}
	if got := m.filterInput.Value(); got != "fo" {
		t.Errorf("filter input = %q on re-open, want the previous query %q", got, "fo")
	}
	m = typeKeys(t, m, "o")
	if got := m.filters[tabPlugins]; got != "foo" {
		t.Errorf("filters[tabPlugins] = %q, want the query refined to %q", got, "foo")
	}
}

func TestFilterTabClosesInputKeepingQuery(t *testing.T) {
	m := modelWithCells(t, okRunner(), installedFoo(true))

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "f", "o")
	m, _ = press(t, m, "tab")

	if m.filterEditing {
		t.Error("filterEditing = true after tab, want the input closed")
	}
	if m.tab != tabMCP {
		t.Errorf("tab = %v after tab key, want the tab switched", m.tab)
	}
	if got := m.filters[tabPlugins]; got != "fo" {
		t.Errorf("filters[tabPlugins] = %q after tab, want %q kept", got, "fo")
	}
}

func TestFilterNarrowsVisibleRows(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "g", "a", "m")

	view := m.View()
	if !strings.Contains(view, "gamma") {
		t.Errorf("View() drops the matching row:\n%s", view)
	}
	for _, gone := range []string{"alpha", "beta"} {
		if strings.Contains(view, gone) {
			t.Errorf("View() still shows the non-matching row %q:\n%s", gone, view)
		}
	}
}

func TestFilterUnfoldsFoldedGroup(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	// Row 0 is the mp group header; enter folds it.
	m, _ = press(t, m, "enter")
	if strings.Contains(m.View(), "alpha") {
		t.Fatalf("fold left the plugin rows visible:\n%s", m.View())
	}

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a", "l")
	if !strings.Contains(m.View(), "alpha") {
		t.Errorf("a folded group hid a matching row:\n%s", m.View())
	}

	m, _ = press(t, m, "esc")
	if strings.Contains(m.View(), "alpha") {
		t.Errorf("fold not restored after the filter was cleared:\n%s", m.View())
	}
}

func TestFilterResetsSelectionToTop(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	// Rows: mp, alpha, beta, other, gamma.
	m = typeKeys(t, m, "j", "j", "j", "j")
	if m.selRow != 4 {
		t.Fatalf("selRow = %d before filtering, want the last row (4)", m.selRow)
	}

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a", "l")

	if m.selRow != 0 {
		t.Errorf("selRow = %d after the query changed, want 0", m.selRow)
	}
	if !strings.Contains(m.View(), "alpha") {
		t.Errorf("View() drops the matching row:\n%s", m.View())
	}
}

func TestNavigationFollowsFilteredRows(t *testing.T) {
	forceANSI(t)
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "b", "e")
	m, _ = press(t, m, "enter")

	// Filtered rows: the mp header and beta — j lands on beta, and a second j
	// has nowhere to go.
	m, _ = press(t, m, "j")
	assertHighlighted(t, m.View(), "beta")
	m, _ = press(t, m, "j")
	assertHighlighted(t, m.View(), "beta")
}

func TestActionOnFilteredRowTargetsSelectedPlugin(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "b", "e")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "j")

	_, cmd := press(t, m, "d")
	if cmd == nil {
		t.Fatal("d on a filtered plugin row produced no command")
	}
	drain(t, cmd)

	calls := pluginCalls(runner, "disable")
	if len(calls) != 1 {
		t.Fatalf("got %d disable calls, want 1 (all calls: %v)", len(calls), runner.Calls)
	}
	want := []string{"plugin", "disable", "--scope", "user", "beta@mp"}
	if !slices.Equal(calls[0].Args, want) {
		t.Errorf("args = %v, want %v", calls[0].Args, want)
	}
}

func TestFilterInputDoesNotBlockConfirmPrompt(t *testing.T) {
	runner := okRunner()
	m := modelWithCells(t, runner, installedFoo(true))
	m.pending = &pendingAction{verb: "uninstall", target: "foo@mp", col: 0}

	m, _ = press(t, m, "/")

	if m.filterEditing {
		t.Error("/ opened the filter input while a confirmation was pending")
	}
	if m.pending != nil {
		t.Error("pending confirmation survived the key, want it resolved (cancelled)")
	}
}
