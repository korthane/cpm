package ui

import (
	"strings"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/model"
)

// withMarketplace attaches a github-source marketplace entry to the data so
// its group header row is configured (and carries commit info) there.
func withMarketplace(data claudecli.PluginData, name, hash, date string) claudecli.PluginData {
	data.Marketplaces = append(data.Marketplaces, claudecli.Marketplace{
		Name: name, Source: "github", Repo: "owner/" + name,
		CommitHash: hash, CommitDate: date,
	})
	return data
}

// installedIn builds PluginData with the named plugins installed in the given
// marketplace.
func installedIn(marketplace string, names ...string) claudecli.PluginData {
	data := claudecli.PluginData{}
	for _, n := range names {
		data.Installed = append(data.Installed, claudecli.InstalledPlugin{
			ID:      claudecli.PluginID{Name: n, Marketplace: marketplace},
			Version: "1.0.0", Enabled: true,
		})
	}
	return data
}

// viewLine returns the first view line containing substr.
func viewLine(t *testing.T, view, substr string) string {
	t.Helper()
	for line := range strings.Lines(view) {
		if strings.Contains(line, substr) {
			return line
		}
	}
	t.Fatalf("no view line contains %q:\n%s", substr, view)
	return ""
}

func TestGroupedViewShowsMarketplaceHeaderAndIndentedPlugins(t *testing.T) {
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0, claudecli.PluginData{})

	view := m.View()
	header := viewLine(t, view, chevronUnfolded+" mp")
	if !strings.Contains(header, "a1b2c3 2026-06-28") {
		t.Errorf("header row missing commit info in the configured column:\n%s", header)
	}
	if !strings.Contains(header, "—") {
		t.Errorf("header row missing — in the unconfigured column:\n%s", header)
	}
	// The group header carries the marketplace, so plugin rows drop it.
	if strings.Contains(view, "foo@mp") {
		t.Errorf("plugin row still shows @marketplace:\n%s", view)
	}
	if !strings.Contains(viewLine(t, view, "foo"), "  foo") {
		t.Errorf("plugin name not indented under its group:\n%s", view)
	}
}

func TestMarketplaceCellRendering(t *testing.T) {
	loaded := &column{status: statusLoaded}
	tests := []struct {
		name string
		col  *column
		cell model.MarketplaceCell
		want string
	}{
		{"column not loaded", &column{}, model.MarketplaceCell{Configured: true, CommitHash: "a1b2c3"}, ""},
		{"unconfigured", loaded, model.MarketplaceCell{}, "—"},
		{"commit info", loaded,
			model.MarketplaceCell{Configured: true, CommitHash: "a1b2c3", CommitDate: "2026-06-28"},
			"a1b2c3 2026-06-28"},
		{"directory source without git info", loaded,
			model.MarketplaceCell{Configured: true, Local: true}, "local"},
		{"git lookup failed", loaded, model.MarketplaceCell{Configured: true}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.col.marketplaceCell(tt.cell); got.text != tt.want {
				t.Errorf("cell text = %q, want %q", got.text, tt.want)
			}
		})
	}
}

func TestGroupedViewDirectorySourceShowsLocal(t *testing.T) {
	p0 := installedFoo(true)
	p0.Marketplaces = []claudecli.Marketplace{{Name: "mp", Source: "directory", Path: "/opt/mp"}}
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	if header := viewLine(t, m.View(), " mp"); !strings.Contains(header, "local") {
		t.Errorf("directory-source header row missing `local`:\n%s", header)
	}
}

func TestFoldCollapsesGroupAndUnfoldRestores(t *testing.T) {
	for _, key := range []string{"enter", "space"} {
		t.Run(key, func(t *testing.T) {
			p0 := withMarketplace(installedIn("mp", "bar", "foo"), "mp", "a1b2c3", "2026-06-28")
			m := modelWithCells(t, &claudecli.FakeRunner{}, p0)
			if m.rowCount() != 3 {
				t.Fatalf("rowCount = %d, want 3 (header + 2 plugins)", m.rowCount())
			}

			// selRow 0 is the group header.
			m, _ = press(t, m, key)
			view := m.View()
			if !strings.Contains(view, chevronFolded+" mp (2 plugins)") {
				t.Errorf("folded header missing chevron and plugin count:\n%s", view)
			}
			if strings.Contains(view, "bar") || strings.Contains(view, "foo") {
				t.Errorf("folded group still shows plugin rows:\n%s", view)
			}
			if m.rowCount() != 1 {
				t.Errorf("rowCount after fold = %d, want 1", m.rowCount())
			}

			m, _ = press(t, m, key)
			view = m.View()
			if !strings.Contains(view, chevronUnfolded+" mp") || strings.Contains(view, "(2 plugins)") {
				t.Errorf("unfold did not restore the header:\n%s", view)
			}
			if !strings.Contains(view, "bar") || !strings.Contains(view, "foo") {
				t.Errorf("unfold did not restore the plugin rows:\n%s", view)
			}
		})
	}
}

func TestFoldClampsSelectionToVisibleRows(t *testing.T) {
	p0 := withMarketplace(installedIn("mp", "a", "b", "c"), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	// Walk down across marketplace and plugin rows, then back to the header.
	for range 3 {
		m, _ = press(t, m, "down")
	}
	if m.selRow != 3 {
		t.Fatalf("selRow after 3 downs = %d, want 3 (last plugin)", m.selRow)
	}
	for range 3 {
		m, _ = press(t, m, "up")
	}

	m, _ = press(t, m, "enter") // fold: only the header row remains
	if m, _ = press(t, m, "down"); m.selRow != 0 {
		t.Errorf("selRow after down on a folded 1-row view = %d, want 0 (clamped)", m.selRow)
	}
}

func TestDownFromFoldedGroupLandsOnNextHeader(t *testing.T) {
	p0 := withMarketplace(withMarketplace(claudecli.PluginData{
		Installed: append(installedIn("amp", "a").Installed, installedIn("bmp", "b").Installed...),
	}, "amp", "", ""), "bmp", "", "")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	m, _ = press(t, m, "enter") // fold amp at selRow 0
	if view := m.View(); !strings.Contains(view, chevronFolded+" amp (1 plugin)") {
		t.Errorf("folded 1-plugin group header wrong:\n%s", view)
	}

	// The next visible row is bmp's header, so a second fold must hit it.
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "enter")
	if view := m.View(); !strings.Contains(view, chevronFolded+" bmp (1 plugin)") {
		t.Errorf("down from a folded group did not land on the next header:\n%s", view)
	}
}

func TestFooterHelpFollowsSelectedRowKind(t *testing.T) {
	const (
		marketHelp = "i: add  u: update  x: remove  enter: fold"
		pluginHelp = "e: enable  d: disable  u: update  x: uninstall  i: install"
	)
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	if view := m.View(); !strings.Contains(view, marketHelp) || strings.Contains(view, pluginHelp) {
		t.Errorf("marketplace row selected, footer should show marketplace actions:\n%s", view)
	}
	m, _ = press(t, m, "down")
	if view := m.View(); !strings.Contains(view, pluginHelp) || strings.Contains(view, marketHelp) {
		t.Errorf("plugin row selected, footer should show plugin actions:\n%s", view)
	}
}

func TestFoldKeysNoopOutsideMarketplaceRows(t *testing.T) {
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	// Enter on a plugin row folds nothing.
	m, _ = press(t, m, "down")
	before := m.View()
	m, _ = press(t, m, "enter")
	if got := m.View(); got != before {
		t.Errorf("enter on a plugin row changed the view:\n%s", got)
	}

	// Enter on the MCP tab folds nothing either.
	m, _ = press(t, m, "tab")
	loaded, _ := m.Update(mcpLoadedMsg{index: 0, gen: m.columns[0].mcpGen,
		servers: []claudecli.MCPServer{{Name: "exa", Target: "url"}}})
	m = loaded.(Model)
	before = m.View()
	m, _ = press(t, m, "enter")
	if got := m.View(); got != before {
		t.Errorf("enter on the MCP tab changed the view:\n%s", got)
	}
	m, _ = press(t, m, "tab")
	if view := m.View(); !strings.Contains(view, "foo") {
		t.Errorf("plugin rows lost after MCP round-trip:\n%s", view)
	}
}
