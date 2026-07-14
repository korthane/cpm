package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
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

	// Row 0 is the marketplace header, where `d` is a no-op even unfiltered:
	// move onto the plugin row so a leaked key would really fire an action.
	m, _ = press(t, m, "j")
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

	// Plugins: alpha, beta, gamma — "be" matches beta alone. The mp header row
	// the filter keeps alongside it is not a match and must not be counted.
	view := m.View()
	if !strings.Contains(view, "filter: be (1/3)") {
		t.Errorf("View() lacks the query and match count:\n%s", view)
	}
	if !strings.Contains(view, "esc: clear") {
		t.Errorf("View() does not say how to clear the filter:\n%s", view)
	}
}

// A marketplace-name match keeps the group whole, so every plugin under it is
// a match — but the header row it kept them under is still not one.
func TestFilterIndicatorCountsPluginsNotMarketplaceHeaders(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	// "mp" matches no plugin name, only the marketplace holding alpha and beta.
	m = typeKeys(t, m, "m", "p")
	m, _ = press(t, m, "enter")

	view := m.View()
	if !strings.Contains(view, "filter: mp (2/3)") {
		t.Errorf("View() counted the marketplace header row as a match:\n%s", view)
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

	// Focusing the input suppresses the action help line, which gives the row
	// back: the filter line and that line cancel out.
	m, _ = press(t, m, "/")
	if view := m.View(); !strings.Contains(view, "… rows 1–5 of 21") {
		t.Errorf("focused input did not give the suppressed help line back to the window:\n%s", view)
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

// TestFilterSurvivesReload pins the filter to the tab, not to a load: reloading
// blanks the columns and refills them asynchronously, and the query must still
// narrow the rebuilt groups.
func TestFilterSurvivesReload(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "g", "a", "m")
	m, _ = press(t, m, "enter")

	m, _ = press(t, m, "r")
	if got := m.filters[tabPlugins]; got != "gam" {
		t.Errorf("filters[tabPlugins] = %q after reload, want the query kept", got)
	}
	if view := m.View(); strings.Contains(view, "alpha") {
		t.Errorf("reloading columns un-applied the filter:\n%s", view)
	}

	reloaded, _ := m.Update(profileLoadedMsg{
		index: 0, gen: m.columns[0].gen, plugins: multiPlugins(),
	})
	m = reloaded.(Model)

	view := m.View()
	if !strings.Contains(view, "gamma") {
		t.Errorf("reloaded data lost the matching row:\n%s", view)
	}
	if strings.Contains(view, "alpha") {
		t.Errorf("filter not applied to the reloaded data:\n%s", view)
	}
}

// TestFilterOnZeroLoadedColumns guards the degenerate case: no profile has
// reported yet, so there are no groups for the filter to narrow.
func TestFilterOnZeroLoadedColumns(t *testing.T) {
	m := New(okRunner(), nil)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = resized.(Model)

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a")
	m, _ = press(t, m, "enter")

	// No rows exist to match, so the query is not what emptied the list: the
	// no-match line would blame the filter for it.
	if view := m.View(); strings.Contains(view, `no plugins match`) {
		t.Errorf("no-match line shown with nothing loaded to match:\n%s", view)
	}
	// The action would index the empty group list if the filter left it stale.
	m, _ = press(t, m, "d")
	if m.pending != nil {
		t.Error("action key started on an empty filtered list, want it ignored")
	}
}

// TestFilterKeepsLoadingAndErrorChromeVisible guards the empty-state gate: a
// reloading or errored column has no rows either, and reporting that as "no
// plugins match" would hide the spinner and swallow the load error.
func TestFilterKeepsLoadingAndErrorChromeVisible(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "g", "a", "m")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "r")

	view := m.View()
	if strings.Contains(view, `no plugins match`) {
		t.Errorf("reload under a filter reported the rows as filtered out:\n%s", view)
	}
	if !strings.Contains(view, "loading") {
		t.Errorf("reload under a filter hid the loading state:\n%s", view)
	}

	errored, _ := m.Update(profileErrMsg{index: 0, gen: m.columns[0].gen, err: errors.New("boom")})
	m = errored.(Model)

	if view := m.View(); !strings.Contains(view, "boom") {
		t.Errorf("a filter hid the column's load error:\n%s", view)
	}
}

// halfLoadedModel builds a two-profile model in which only p0 has loaded, so
// p1 still renders a spinner and contributes no rows.
func halfLoadedModel(t *testing.T, data claudecli.PluginData) Model {
	t.Helper()
	profiles := []config.Profile{
		{Path: "/h/p0", Label: "p0"},
		{Path: "/h/p1", Label: "p1"},
	}
	m := New(okRunner(), profiles)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = resized.(Model)
	loaded, _ := m.Update(profileLoadedMsg{index: 0, plugins: data})
	return loaded.(Model)
}

// TestNoMatchLineKeepsUnloadedColumnVisible pins the empty state against a
// mixed column set: p0 has rows, so the unfiltered total is non-zero, but
// blaming the query would replace the table and hide p1's spinner — and then
// its error line, leaving a permanently failing profile invisible.
func TestNoMatchLineKeepsUnloadedColumnVisible(t *testing.T) {
	m := halfLoadedModel(t, multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "z", "z", "z")
	m, _ = press(t, m, "enter")

	view := m.View()
	if strings.Contains(view, "no plugins match") {
		t.Errorf("a no-match query replaced the table while p1 was loading:\n%s", view)
	}
	if !strings.Contains(view, "loading") {
		t.Errorf("a no-match query hid the loading column:\n%s", view)
	}

	errored, _ := m.Update(profileErrMsg{index: 1, gen: m.columns[1].gen, err: errors.New("boom")})
	m = errored.(Model)

	if view := m.View(); !strings.Contains(view, "boom") {
		t.Errorf("a no-match query hid the column's load error:\n%s", view)
	}
}

// TestFoldKeyIgnoredWhileFiltering pins the fold no-op: activeFolds is nil
// under a filter, so a fold recorded now would be invisible until the filter
// is cleared and would then hide rows the user never folded.
func TestFoldKeyIgnoredWhileFiltering(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a")
	m, _ = press(t, m, "enter")

	before := m.View()
	m, _ = press(t, m, "enter") // on the "mp" marketplace header row
	if after := m.View(); after != before {
		t.Errorf("fold key changed the filtered view:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if m.folded["mp"] {
		t.Error("fold key recorded a fold while filtering, want it ignored")
	}

	m, _ = press(t, m, "esc")
	if view := m.View(); !strings.Contains(view, "alpha") {
		t.Errorf("clearing the filter revealed a fold the user never made:\n%s", view)
	}
}

// TestHelpDropsFoldKeyWhileFiltering: enter does not fold under a filter, so
// the help line must not advertise it.
func TestHelpDropsFoldKeyWhileFiltering(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	if view := m.View(); !strings.Contains(view, "enter: fold") {
		t.Fatalf("unfiltered help lacks the fold key:\n%s", view)
	}

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a")
	m, _ = press(t, m, "enter")

	if view := m.View(); strings.Contains(view, "enter: fold") {
		t.Errorf("help advertises fold while filtering, where it is a no-op:\n%s", view)
	}
}

// TestFilterBackspaceToEmptyRestoresFolds walks the query back to empty, which
// must restore both the full row set and the fold state the filter suspended.
func TestFilterBackspaceToEmptyRestoresFolds(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "enter") // fold "mp" before any filter exists
	if view := m.View(); strings.Contains(view, "alpha") {
		t.Fatalf("fold did not hide the group's plugins:\n%s", view)
	}

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a", "l")
	if view := m.View(); !strings.Contains(view, "alpha") {
		t.Fatalf("filter did not unfold the group:\n%s", view)
	}

	m = typeKeys(t, m, "backspace", "backspace")

	if got := m.filters[tabPlugins]; got != "" {
		t.Errorf("filters[tabPlugins] = %q after backspacing to empty, want %q", got, "")
	}
	view := m.View()
	if !strings.Contains(view, "gamma") {
		t.Errorf("clearing the query did not restore the full list:\n%s", view)
	}
	if strings.Contains(view, "alpha") {
		t.Errorf("clearing the query did not restore the suspended fold:\n%s", view)
	}
}

// TestFilterStatusClearedByTyping: the transient status is dismissed by the
// next key everywhere else, so it must not linger under the focused input and
// read as a reply to what is being typed.
func TestFilterStatusClearedByTyping(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	// An async action result can land while the input is already focused.
	m.setStatus("cannot disable alpha in p0", true)
	m = typeKeys(t, m, "a")

	if m.status != "" {
		t.Errorf("status = %q while typing a filter, want it cleared", m.status)
	}
}

// TestFilterMatchingOnlyMarketplaceHeader covers a filtered list of header rows
// only: the plugin-less group matches on its own name, and a plugin action key
// on that header row must be refused rather than index a plugin that is not
// there.
func TestFilterMatchingOnlyMarketplaceHeader(t *testing.T) {
	data := multiPlugins()
	data.Marketplaces = []claudecli.Marketplace{{Name: "solo", Source: "directory"}}
	m := modelWithCells(t, okRunner(), data)

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "s", "o", "l")
	m, _ = press(t, m, "enter")

	view := m.View()
	if !strings.Contains(view, "solo") {
		t.Errorf("marketplace-only match dropped its header row:\n%s", view)
	}
	for _, gone := range []string{"alpha", "beta", "gamma"} {
		if strings.Contains(view, gone) {
			t.Errorf("View() still shows the non-matching plugin %q:\n%s", gone, view)
		}
	}

	m, _ = press(t, m, "d")
	if m.pending != nil {
		t.Error("plugin action started on a marketplace header row, want it ignored")
	}
}

// TestFilterQueryIsLiteralText: the query is fed to a fuzzy matcher, not to a
// regexp or glob engine, so metacharacters match only themselves.
func TestFilterQueryIsLiteralText(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, ".", "*")

	view := m.View()
	if !strings.Contains(view, `no plugins match ".*"`) {
		t.Errorf("%q was treated as a pattern, not literal text:\n%s", ".*", view)
	}
}

// TestWhitespaceOnlyQueryIsNoFilter: the filters trim the query before matching,
// so a query of spaces hides nothing. It must not count as an active filter
// either, or it would suspend folding and spend a chrome line on an indicator
// for a filter with no effect — with no visible cause, since the query prints as
// empty text.
func TestWhitespaceOnlyQueryIsNoFilter(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "enter") // fold "mp" before any filter exists
	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "space")
	m, _ = press(t, m, "enter")

	if got := m.filters[tabPlugins]; got != "" {
		t.Errorf("filters[tabPlugins] = %q for a whitespace-only query, want %q", got, "")
	}
	if m.filterVisible() {
		t.Error("a whitespace-only query renders the filter line, want it treated as no filter")
	}
	view := m.View()
	if strings.Contains(view, "alpha") {
		t.Errorf("a whitespace-only query suspended the fold it does not narrow:\n%s", view)
	}
	if !strings.Contains(view, "enter: fold") {
		t.Errorf("help dropped the fold key under a whitespace-only query:\n%s", view)
	}
}

// TestFilterQueryStoredTrimmed: a trailing space is dropped on the way in, so
// the indicator shows the query that is actually being matched.
func TestFilterQueryStoredTrimmed(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "a", "l", "space")
	m, _ = press(t, m, "enter")

	if got := m.filters[tabPlugins]; got != "al" {
		t.Errorf("filters[tabPlugins] = %q, want %q", got, "al")
	}
	if view := m.View(); !strings.Contains(view, "alpha") {
		t.Errorf("a trailing space emptied the table:\n%s", view)
	}
}

// TestFilterInputScrollsOnNarrowTerminal: bubbles only windows the value once
// the input has a width, so a query longer than the line must scroll rather
// than render in full and have its tail — the part being typed, and the cursor
// with it — chopped off by fitWidth.
func TestFilterInputScrollsOnNarrowTerminal(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m = resized.(Model)

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, strings.Split("abcdefghijklmnopqrstuvwxyz0123456789", "")...)

	view := m.View()
	i := lineOf(view, filterPrompt)
	if i < 0 {
		t.Fatalf("view has no filter input line:\n%s", view)
	}
	line := strings.Split(view, "\n")[i]
	if !strings.Contains(line, "6789") {
		t.Errorf("input did not scroll: the query's tail is off-screen:\n%q", line)
	}
}

// TestFilterInputWidthTracksTerminal: the input's width is the line minus the
// prompt and a cell for the cursor, and it has to follow a resize.
func TestFilterInputWidthTracksTerminal(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m = resized.(Model)

	if want := 30 - len(filterPrompt) - 1; m.filterInput.Width != want {
		t.Errorf("filterInput.Width = %d, want %d", m.filterInput.Width, want)
	}

	// A terminal narrower than the prompt must not drive the width negative.
	resized, _ = m.Update(tea.WindowSizeMsg{Width: 2, Height: 24})
	m = resized.(Model)
	if m.filterInput.Width < 0 {
		t.Errorf("filterInput.Width = %d on a 2-column terminal, want >= 0", m.filterInput.Width)
	}
}

// TestFilterCtrlVReachesInput: ctrl+v is the input's paste key, so it must be
// handed to the input (which answers with a command carrying the clipboard)
// rather than typed into the query as literal text.
func TestFilterCtrlVReachesInput(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m, cmd := press(t, m, "ctrl+v")

	if got := m.filterInput.Value(); got != "" {
		t.Errorf("ctrl+v was typed as text: value = %q, want empty", got)
	}
	if cmd == nil {
		t.Error("ctrl+v produced no command, want the input's paste command")
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
