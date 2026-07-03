package ui

import (
	"strings"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
)

func TestVimKeysMoveRowSelection(t *testing.T) {
	// mp header + 3 plugin rows = 4 rows.
	p0 := withMarketplace(installedIn("mp", "a", "b", "c"), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	for want := 1; want <= 3; want++ {
		if m, _ = press(t, m, "j"); m.selRow != want {
			t.Fatalf("selRow after j = %d, want %d", m.selRow, want)
		}
	}
	if m, _ = press(t, m, "j"); m.selRow != 3 {
		t.Errorf("selRow after j on the last row = %d, want 3 (clamped)", m.selRow)
	}
	for want := 2; want >= 0; want-- {
		if m, _ = press(t, m, "k"); m.selRow != want {
			t.Fatalf("selRow after k = %d, want %d", m.selRow, want)
		}
	}
	if m, _ = press(t, m, "k"); m.selRow != 0 {
		t.Errorf("selRow after k on the first row = %d, want 0 (clamped)", m.selRow)
	}

	// j must respect fold-aware visibleRows, exactly like down.
	m, _ = press(t, m, "enter") // fold: only the header row remains
	if m, _ = press(t, m, "j"); m.selRow != 0 {
		t.Errorf("selRow after j on a folded 1-row view = %d, want 0 (clamped)", m.selRow)
	}
}

func TestVimKeysMoveColumnSelection(t *testing.T) {
	m := modelWithCells(t, &claudecli.FakeRunner{},
		installedFoo(true), claudecli.PluginData{})

	if m, _ = press(t, m, "l"); m.selCol != 1 {
		t.Fatalf("selCol after l = %d, want 1", m.selCol)
	}
	if m, _ = press(t, m, "l"); m.selCol != 1 {
		t.Errorf("selCol after l on the last column = %d, want 1 (clamped)", m.selCol)
	}
	if m, _ = press(t, m, "h"); m.selCol != 0 {
		t.Fatalf("selCol after h = %d, want 0", m.selCol)
	}
	if m, _ = press(t, m, "h"); m.selCol != 0 {
		t.Errorf("selCol after h on the first column = %d, want 0 (clamped)", m.selCol)
	}
}

func TestVimKeysWorkOnMCPTab(t *testing.T) {
	servers := []claudecli.MCPServer{
		{Name: "exa", Target: "https://mcp.exa.ai/mcp"},
		{Name: "fs", Target: "npx server-fs"},
	}
	m := mcpModelWithServers(t, &claudecli.FakeRunner{}, servers, servers)

	if m, _ = press(t, m, "j"); m.selRow != 1 {
		t.Fatalf("selRow after j on MCP tab = %d, want 1", m.selRow)
	}
	if m, _ = press(t, m, "j"); m.selRow != 1 {
		t.Errorf("selRow after j on the last MCP row = %d, want 1 (clamped)", m.selRow)
	}
	if m, _ = press(t, m, "k"); m.selRow != 0 {
		t.Fatalf("selRow after k on MCP tab = %d, want 0", m.selRow)
	}
	if m, _ = press(t, m, "l"); m.selCol != 1 {
		t.Fatalf("selCol after l on MCP tab = %d, want 1", m.selCol)
	}
	if m, _ = press(t, m, "h"); m.selCol != 0 {
		t.Fatalf("selCol after h on MCP tab = %d, want 0", m.selCol)
	}
}

func TestFooterHelpListsVimAliases(t *testing.T) {
	m := modelWithCells(t, &claudecli.FakeRunner{}, installedFoo(true))

	if view := m.View(); !strings.Contains(view, "←/→/h/l ↑/↓/j/k: select") {
		t.Errorf("footer help does not list the vim aliases:\n%s", view)
	}
}

func TestVimKeysDuringConfirmationCancelLikeArrows(t *testing.T) {
	// handleConfirmKey resolves every key before navigation: arrows cancel the
	// prompt without moving the selection, and the vim aliases must match.
	for _, key := range []string{"j", "k", "h", "l"} {
		t.Run(key, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			m := modelWithCells(t, runner, installedFoo(true), installedFoo(true))
			m, _ = press(t, m, "down") // onto foo's plugin row
			m, _ = press(t, m, "x")    // arm the uninstall confirmation
			if m.pending == nil {
				t.Fatal("uninstall confirmation not armed")
			}

			m, cmd := press(t, m, key)
			if cmd != nil || len(pluginCalls(runner, "uninstall")) != 0 {
				t.Fatalf("%q during confirmation ran the pending action", key)
			}
			if m.pending != nil {
				t.Fatalf("%q during confirmation left the prompt pending", key)
			}
			if view := m.View(); !strings.Contains(view, "cancelled") {
				t.Errorf("%q during confirmation did not cancel:\n%s", key, view)
			}
			if m.selRow != 1 || m.selCol != 0 {
				t.Errorf("%q during confirmation moved the selection to (%d,%d), want (1,0)",
					key, m.selRow, m.selCol)
			}
		})
	}
}
