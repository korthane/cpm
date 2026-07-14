package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
)

// filterModelBothTabs loads plugin rows (multiPlugins) and MCP servers into one
// model, so a test can filter one tab and observe the other.
func filterModelBothTabs(t *testing.T, runner *claudecli.FakeRunner) Model {
	t.Helper()
	m := modelWithCells(t, runner, multiPlugins())
	m, _ = switchToMCP(t, m)
	loaded, _ := m.Update(mcpLoadedMsg{index: 0, gen: m.columns[0].mcpGen, servers: []claudecli.MCPServer{
		{Name: "exa", Target: "https://mcp.exa.ai/mcp"},
		{Name: "github", Target: "https://api.github.com/mcp"},
		{Name: "linear", Target: "https://mcp.linear.app/mcp"},
	}})
	return loaded.(Model)
}

func TestFilterNarrowsMCPRows(t *testing.T) {
	m := filterModelBothTabs(t, okRunner())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "g", "i", "t")

	view := m.View()
	if !strings.Contains(view, "github") {
		t.Errorf("View() drops the matching server:\n%s", view)
	}
	for _, gone := range []string{"exa", "linear"} {
		if strings.Contains(view, gone) {
			t.Errorf("View() still shows the non-matching server %q:\n%s", gone, view)
		}
	}
}

func TestFilterIsPerTab(t *testing.T) {
	m := modelWithCells(t, okRunner(), multiPlugins())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "b", "e")
	m, _ = press(t, m, "enter")

	m, _ = switchToMCP(t, m)
	loaded, _ := m.Update(mcpLoadedMsg{index: 0, gen: m.columns[0].mcpGen, servers: []claudecli.MCPServer{
		{Name: "exa"}, {Name: "github"},
	}})
	m = loaded.(Model)

	view := m.View()
	if got := m.filters[tabMCP]; got != "" {
		t.Errorf("filters[tabMCP] = %q, want the MCP tab unfiltered", got)
	}
	for _, want := range []string{"exa", "github"} {
		if !strings.Contains(view, want) {
			t.Errorf("MCP tab hides %q — the plugin filter leaked:\n%s", want, view)
		}
	}

	back, _ := press(t, m, "shift+tab")
	if got := back.filters[tabPlugins]; got != "be" {
		t.Errorf("filters[tabPlugins] = %q after the round trip, want %q", got, "be")
	}
	if view := back.View(); strings.Contains(view, "gamma") {
		t.Errorf("plugin filter not reapplied after switching back:\n%s", view)
	}
}

func TestFilterTabSwitchWhileEditingKeepsBothQueries(t *testing.T) {
	m := filterModelBothTabs(t, okRunner())

	// The plugins tab is filtered first, then the MCP query is typed and the
	// tab key switches away with the input still focused.
	back, _ := press(t, m, "shift+tab")
	back, _ = press(t, back, "/")
	back = typeKeys(t, back, "b", "e")
	back, _ = press(t, back, "enter")
	m, _ = press(t, back, "tab")

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "e", "x")
	m, _ = press(t, m, "tab")

	if m.filterEditing {
		t.Error("filterEditing = true after tab, want the input closed")
	}
	if got := m.filters[tabMCP]; got != "ex" {
		t.Errorf("filters[tabMCP] = %q, want %q kept", got, "ex")
	}
	if got := m.filters[tabPlugins]; got != "be" {
		t.Errorf("filters[tabPlugins] = %q, want %q kept", got, "be")
	}
}

func TestMCPFilterIndicatorAndEmptyState(t *testing.T) {
	m := filterModelBothTabs(t, okRunner())

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "e", "x")
	m, _ = press(t, m, "enter")

	if view := m.View(); !strings.Contains(view, "filter: ex (1/3)") {
		t.Errorf("View() lacks the query and match count:\n%s", view)
	}

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "z", "z", "z")

	view := m.View()
	if !strings.Contains(view, `no MCP servers match "exzzz"`) {
		t.Errorf("View() lacks the empty-result line:\n%s", view)
	}
	if strings.Contains(view, "github") {
		t.Errorf("View() still renders rows for a query that matches nothing:\n%s", view)
	}
}

func TestMCPRemoveOnFilteredRowTargetsSelectedServer(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := filterModelBothTabs(t, runner)

	m, _ = press(t, m, "/")
	m = typeKeys(t, m, "l", "i", "n")
	m, _ = press(t, m, "enter")

	m, _ = press(t, m, "x")
	_, cmd := press(t, m, "y")
	if cmd == nil {
		t.Fatal("confirmed remove on a filtered row produced no command")
	}
	drain(t, cmd)

	calls := mcpRemoveCalls(runner)
	if len(calls) != 1 {
		t.Fatalf("got %d remove calls, want 1 (all calls: %v)", len(calls), runner.Calls)
	}
	want := []string{"mcp", "remove", "--scope", "user", "linear"}
	if !slices.Equal(calls[0].Args, want) {
		t.Errorf("args = %v, want %v", calls[0].Args, want)
	}
}
