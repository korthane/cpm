package ui

import (
	"fmt"
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

// lineOf returns the index of the first view line containing want, or -1.
func lineOf(view, want string) int {
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
}

func TestFilterInputRendersAboveTableHeader(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")

	view := m.View()
	input, header := lineOf(view, filterPrompt), lineOf(view, "/h/p0")
	if input < 0 || header < 0 {
		t.Fatalf("input line %d, header line %d — both must render:\n%s", input, header, view)
	}
	if input >= header {
		t.Errorf("filter input on line %d, table header on line %d, want the input above:\n%s",
			input, header, view)
	}
}

func TestFilterIndicatorShownWhileInputClosed(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "b", "e")
	m, _ = press(t, m, "enter")

	// Rows: mp, alpha, beta, other, gamma — "be" keeps the mp header and beta.
	view := m.View()
	if !strings.Contains(view, "filter: be (2/5)") {
		t.Errorf("View() lacks the query and match count:\n%s", view)
	}
	if !strings.Contains(view, "esc: clear") {
		t.Errorf("View() does not say how to clear the filter:\n%s", view)
	}
}

func TestFilterIndicatorGoneWhenQueryCleared(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "b", "e")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "esc")

	if view := m.View(); strings.Contains(view, filterPrompt) {
		t.Errorf("filter line still rendered after the query was cleared:\n%s", view)
	}
}

func TestFilterWithNoMatchesShowsMessage(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "z", "z", "z")

	view := m.View()
	if !strings.Contains(view, `no plugins match "zzz"`) {
		t.Errorf("View() lacks the empty-result line:\n%s", view)
	}
	if strings.Contains(view, "alpha") {
		t.Errorf("View() still renders rows for a query that matches nothing:\n%s", view)
	}
}

// TestFilterLineShrinksRowWindow guards the scroll window: the filter line is
// one more non-body line, so the same terminal height must fit one row fewer.
func TestFilterLineShrinksRowWindow(t *testing.T) {
	installed := make([]claudecli.InstalledPlugin, 20)
	for i := range installed {
		installed[i] = claudecli.InstalledPlugin{
			ID:      claudecli.PluginID{Name: fmt.Sprintf("plug%02d", i), Marketplace: "mp"},
			Version: "1.0.0", Enabled: true,
		}
	}
	m := modelWithCells(t, okRunner(), claudecli.PluginData{Installed: installed})
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = resized.(Model)

	if view := m.View(); !strings.Contains(view, "… rows 1–5 of 21") {
		t.Fatalf("unfiltered window is not 5 rows:\n%s", view)
	}

	// "p" matches the mp marketplace, so every row survives the filter and only
	// the extra filter line can change the window.
	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "p")
	m, _ = press(t, m, "enter")

	if view := m.View(); !strings.Contains(view, "… rows 1–4 of 21") {
		t.Errorf("filter line did not shrink the row window to 4:\n%s", view)
	}
}

func TestHelpAdvertisesFilterKey(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	if view := m.View(); !strings.Contains(view, "/: filter") {
		t.Errorf("idle help line does not advertise the filter key:\n%s", view)
	}
}

func TestHelpWhileFilteringShowsOnlyWorkingKeys(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")

	view := m.View()
	for _, want := range []string{"enter: apply", "esc: cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("editing help line lacks %q:\n%s", want, view)
		}
	}
	// Every one of these types a literal rune while the input is focused.
	for _, gone := range []string{"q: quit", "r: reload", "i: add", "e: enable"} {
		if strings.Contains(view, gone) {
			t.Errorf("editing help line still advertises %q:\n%s", gone, view)
		}
	}
}

func TestHelpRestoredAfterFilterApplied(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "b", "e")
	m, _ = press(t, m, "enter")

	view := m.View()
	if !strings.Contains(view, "q: quit") {
		t.Errorf("navigation help not restored once the input closed:\n%s", view)
	}
	if strings.Contains(view, "enter: apply") {
		t.Errorf("editing help line still rendered with the input closed:\n%s", view)
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
