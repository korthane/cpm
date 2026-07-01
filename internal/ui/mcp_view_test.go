package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/model"
)

// mcpRunner extends okRunner with a canned `mcp list` response.
func mcpRunner() *claudecli.FakeRunner {
	r := okRunner()
	r.Responses["mcp list"] = claudecli.FakeResponse{
		Stdout: []byte("Checking MCP server health…\n\nexa: https://mcp.exa.ai/mcp - ✔ Connected\n"),
	}
	return r
}

func switchToMCP(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.tab != tabMCP {
		t.Fatalf("tab after switch = %v, want MCP", got.tab)
	}
	return got, cmd
}

func TestPinnedMCPColumnWidthCoversHiddenRows(t *testing.T) {
	// The header is padded to the widest of ALL rows, not just the visible
	// window, so the pinned column does not jump while scrolling.
	rows := []model.MCPRow{
		{Name: "s"},
		{Name: "a-much-longer-server-name"},
	}

	col := pinnedMCPColumn(rows, 0, 1)

	if len(col.cells) != 1 {
		t.Fatalf("got %d cells, want the visible window only (1)", len(col.cells))
	}
	if got, want := lipgloss.Width(col.header[2].text), lipgloss.Width(rows[1].Name); got < want {
		t.Errorf("header width %d does not cover the widest hidden row (%d)", got, want)
	}
}

func TestSwitchToMCPTabFiresLazyLoadPerProfile(t *testing.T) {
	m := New(mcpRunner(), testProfiles)

	m, cmd := switchToMCP(t, m)
	if cmd == nil {
		t.Fatal("first switch to MCP tab produced no load command")
	}

	var loads int
	seen := map[int]bool{}
	for _, msg := range drain(t, cmd) {
		if loaded, ok := msg.(mcpLoadedMsg); ok {
			loads++
			seen[loaded.index] = true
		}
	}
	if loads != len(testProfiles) {
		t.Errorf("got %d mcpLoadedMsg, want %d", loads, len(testProfiles))
	}
	for i := range testProfiles {
		if !seen[i] {
			t.Errorf("no mcpLoadedMsg for profile %d", i)
		}
	}

	// Cycling away and back must not re-fire the loads: lazy means once.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	_, cmd = switchToMCP(t, m)
	for _, msg := range drain(t, cmd) {
		if _, ok := msg.(mcpLoadedMsg); ok {
			t.Fatal("second switch to MCP tab re-fired the load")
		}
	}
}

func TestMCPTabNotLoadedBeforeFirstView(t *testing.T) {
	m := New(mcpRunner(), testProfiles)
	for _, msg := range drain(t, m.Init()) {
		if _, ok := msg.(mcpLoadedMsg); ok {
			t.Fatal("Init fired an MCP load; MCP must load lazily on first view")
		}
	}
}

func TestMCPLoadedFlipsOnlyThatColumn(t *testing.T) {
	m := New(mcpRunner(), testProfiles)
	m, _ = switchToMCP(t, m)

	updated, _ := m.Update(mcpLoadedMsg{
		index:   1,
		servers: []claudecli.MCPServer{{Name: "exa", Target: "url"}},
	})
	got := updated.(Model)

	if got.columns[0].mcpStatus != statusLoading {
		t.Errorf("column 0 mcp status = %v, want loading", got.columns[0].mcpStatus)
	}
	if got.columns[1].mcpStatus != statusLoaded {
		t.Errorf("column 1 mcp status = %v, want loaded", got.columns[1].mcpStatus)
	}
	if len(got.columns[1].mcp) != 1 {
		t.Errorf("column 1 has %d servers, want 1", len(got.columns[1].mcp))
	}
}

func TestMCPViewShowsSpinnerWhileLoading(t *testing.T) {
	m := New(mcpRunner(), testProfiles)
	m, _ = switchToMCP(t, m)

	if view := m.View(); !strings.Contains(view, "loading…") {
		t.Errorf("MCP view while loading missing spinner/loading marker:\n%s", view)
	}
}

func TestMCPViewShowsPresentAndAbsentCells(t *testing.T) {
	m := New(mcpRunner(), testProfiles)
	m, _ = switchToMCP(t, m)

	updated, _ := m.Update(mcpLoadedMsg{
		index:   0,
		servers: []claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(mcpLoadedMsg{
		index:   1,
		servers: []claudecli.MCPServer{{Name: "atlassian", Target: "https://mcp.atlassian.com/v1/mcp"}},
	})
	m = updated.(Model)

	view := m.View()
	for _, want := range []string{"mcp server", "exa", "atlassian", "—"} {
		if !strings.Contains(view, want) {
			t.Errorf("MCP view missing %q:\n%s", want, view)
		}
	}
}

func TestMCPErrShownInView(t *testing.T) {
	m := New(mcpRunner(), testProfiles)
	m, _ = switchToMCP(t, m)

	updated, _ := m.Update(mcpErrMsg{index: 0, err: errors.New("mcp boom")})
	m = updated.(Model)

	if m.columns[0].mcpStatus != statusError {
		t.Errorf("column 0 mcp status = %v, want error", m.columns[0].mcpStatus)
	}
	if view := m.View(); !strings.Contains(view, "mcp boom") {
		t.Errorf("MCP view missing error text:\n%s", view)
	}
}

func TestReloadOnMCPTabRefiresMCPLoads(t *testing.T) {
	m := New(mcpRunner(), testProfiles)
	m, cmd := switchToMCP(t, m)
	for _, msg := range drain(t, cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)

	for i := range m.columns {
		if m.columns[i].mcpStatus != statusLoading {
			t.Errorf("column %d mcp status after reload = %v, want loading", i, m.columns[i].mcpStatus)
		}
	}
	var loads int
	for _, msg := range drain(t, cmd) {
		if _, ok := msg.(mcpLoadedMsg); ok {
			loads++
		}
	}
	if loads != len(testProfiles) {
		t.Errorf("reload fired %d MCP loads, want %d", loads, len(testProfiles))
	}
}

func TestMCPLoadErrorProducesErrMsg(t *testing.T) {
	runner := mcpRunner()
	runner.Responses["mcp list"] = claudecli.FakeResponse{Err: errors.New("boom")}
	m := New(runner, testProfiles[:1])

	_, cmd := switchToMCP(t, m)
	for _, msg := range drain(t, cmd) {
		if errMsg, ok := msg.(mcpErrMsg); ok {
			if errMsg.index != 0 {
				t.Errorf("index = %d, want 0", errMsg.index)
			}
			if errMsg.err == nil {
				t.Error("err is nil, want boom")
			}
			return
		}
	}
	t.Fatal("no mcpErrMsg produced")
}

func TestSpinnerTickAliveWhileMCPLoading(t *testing.T) {
	m := New(mcpRunner(), testProfiles[:1])
	// Plugins loaded, MCP still loading: the column spinner must keep ticking.
	loaded, _ := m.Update(profileLoadedMsg{index: 0})
	m = loaded.(Model)
	m, _ = switchToMCP(t, m)

	_, cmd := m.Update(spinner.TickMsg{ID: m.spinner.ID()})
	if cmd == nil {
		t.Error("tick died while MCP column is still loading")
	}

	updated, _ := m.Update(mcpLoadedMsg{index: 0})
	m = updated.(Model)
	_, cmd = m.Update(spinner.TickMsg{ID: m.spinner.ID()})
	if cmd != nil {
		t.Error("tick survived after both plugin and MCP data loaded")
	}
}
