package model

import (
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
)

func githubMarket(name, repo string) claudecli.Marketplace {
	return claudecli.Marketplace{Name: name, Source: "github", Repo: repo}
}

func TestBuildPluginGroupsGroupingAndSorting(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{
			Installed: []claudecli.InstalledPlugin{
				{ID: id("zeta", "beta-market"), Version: "1.0.0", Enabled: true},
				{ID: id("adder", "beta-market"), Version: "1.0.0", Enabled: true},
			},
			Marketplaces: []claudecli.Marketplace{
				githubMarket("beta-market", "owner/beta"),
				githubMarket("alpha-market", "owner/alpha"),
			},
		},
		{
			Installed: []claudecli.InstalledPlugin{
				{ID: id("tool", "alpha-market"), Version: "2.0.0", Enabled: true},
			},
			Marketplaces: []claudecli.Marketplace{
				githubMarket("alpha-market", "owner/alpha"),
			},
		},
	}

	groups := BuildPluginGroups(perProfile, nil)

	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].Marketplace.Name != "alpha-market" || groups[1].Marketplace.Name != "beta-market" {
		t.Errorf("group order: got %q, %q; want alpha-market, beta-market",
			groups[0].Marketplace.Name, groups[1].Marketplace.Name)
	}
	if len(groups[0].Plugins) != 1 || groups[0].Plugins[0].ID != id("tool", "alpha-market") {
		t.Errorf("alpha-market plugins: got %+v, want [tool]", groups[0].Plugins)
	}
	wantBeta := []claudecli.PluginID{id("adder", "beta-market"), id("zeta", "beta-market")}
	if len(groups[1].Plugins) != len(wantBeta) {
		t.Fatalf("beta-market: got %d plugins, want %d", len(groups[1].Plugins), len(wantBeta))
	}
	for i, w := range wantBeta {
		if groups[1].Plugins[i].ID != w {
			t.Errorf("beta-market plugin %d: got %v, want %v", i, groups[1].Plugins[i].ID, w)
		}
	}
}

func TestBuildPluginGroupsMarketplaceCells(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{Marketplaces: []claudecli.Marketplace{{
			Name:       "m",
			Source:     "github",
			Repo:       "owner/m",
			CommitHash: "a1b2c3",
			CommitDate: "2026-06-28",
		}}},
		{Marketplaces: []claudecli.Marketplace{{
			Name:   "m",
			Source: "github",
			Repo:   "owner/m",
			// Git info can fail per profile; cells stay blank there.
		}}},
		{}, // marketplace not configured here
	}

	groups := BuildPluginGroups(perProfile, nil)

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	wantCells := []MarketplaceCell{
		{Configured: true, CommitHash: "a1b2c3", CommitDate: "2026-06-28"},
		{Configured: true},
		{},
	}
	cells := groups[0].Marketplace.Cells
	if len(cells) != len(wantCells) {
		t.Fatalf("got %d cells, want %d", len(cells), len(wantCells))
	}
	for i, w := range wantCells {
		if cells[i] != w {
			t.Errorf("cell %d: got %+v, want %+v", i, cells[i], w)
		}
	}
}

func TestBuildPluginGroupsOrphanedPlugin(t *testing.T) {
	// A plugin whose marketplace is configured in no profile still gets a
	// group so it renders; the marketplace row is unconfigured everywhere.
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("stray", "gone-market"), Version: "1.0.0", Enabled: true},
		}},
		{},
	}

	groups := BuildPluginGroups(perProfile, nil)

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	row := groups[0].Marketplace
	if row.Name != "gone-market" {
		t.Errorf("group name = %q, want gone-market", row.Name)
	}
	if row.SourceArg != "" || row.SourceConflict {
		t.Errorf("orphan row source: got %+v, want empty arg and no conflict", row)
	}
	if len(row.Cells) != 2 {
		t.Fatalf("got %d cells, want 2", len(row.Cells))
	}
	for i, c := range row.Cells {
		if c.Configured {
			t.Errorf("cell %d configured, want unconfigured", i)
		}
	}
	if len(groups[0].Plugins) != 1 {
		t.Errorf("got %d plugins, want 1", len(groups[0].Plugins))
	}
}

func TestBuildPluginGroupsPluginlessMarketplace(t *testing.T) {
	// A configured marketplace with no installed plugins still gets a row so
	// update/remove work on it.
	perProfile := []claudecli.PluginData{
		{Marketplaces: []claudecli.Marketplace{githubMarket("empty-market", "owner/e")}},
	}

	groups := BuildPluginGroups(perProfile, nil)

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if groups[0].Marketplace.Name != "empty-market" {
		t.Errorf("group name = %q, want empty-market", groups[0].Marketplace.Name)
	}
	if len(groups[0].Plugins) != 0 {
		t.Errorf("got %d plugins, want 0", len(groups[0].Plugins))
	}
	if !groups[0].Marketplace.Cells[0].Configured {
		t.Error("cell 0 unconfigured, want configured")
	}
}

func TestBuildPluginGroupsSourceArgResolution(t *testing.T) {
	tests := []struct {
		name         string
		markets      [][]claudecli.Marketplace // per profile
		wantArg      string
		wantConflict bool
	}{
		{
			name: "profiles agree",
			markets: [][]claudecli.Marketplace{
				{githubMarket("m", "owner/m")},
				{githubMarket("m", "owner/m")},
			},
			wantArg: "owner/m",
		},
		{
			name: "single profile git url",
			markets: [][]claudecli.Marketplace{
				{{Name: "m", Source: "git", URL: "https://example.com/m.git"}},
				{},
			},
			wantArg: "https://example.com/m.git",
		},
		{
			name: "directory path",
			markets: [][]claudecli.Marketplace{
				{{Name: "m", Source: "directory", Path: "/opt/m"}},
			},
			wantArg: "/opt/m",
		},
		{
			name: "unknown source in one profile does not erase the known one",
			markets: [][]claudecli.Marketplace{
				{{Name: "m", Source: "weird"}},
				{githubMarket("m", "owner/m")},
			},
			wantArg: "owner/m",
		},
		{
			name: "profiles disagree",
			markets: [][]claudecli.Marketplace{
				{githubMarket("m", "owner/one")},
				{githubMarket("m", "owner/two")},
			},
			wantArg:      "",
			wantConflict: true,
		},
		{
			name: "conflict is not repaired by a later matching profile",
			markets: [][]claudecli.Marketplace{
				{githubMarket("m", "owner/one")},
				{githubMarket("m", "owner/two")},
				{githubMarket("m", "owner/one")},
			},
			wantArg:      "",
			wantConflict: true,
		},
		{
			name: "no usable source anywhere",
			markets: [][]claudecli.Marketplace{
				{{Name: "m", Source: "weird"}},
			},
			wantArg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perProfile := make([]claudecli.PluginData, len(tt.markets))
			for i, mkts := range tt.markets {
				perProfile[i] = claudecli.PluginData{Marketplaces: mkts}
			}

			groups := BuildPluginGroups(perProfile, nil)

			if len(groups) != 1 {
				t.Fatalf("got %d groups, want 1", len(groups))
			}
			row := groups[0].Marketplace
			if row.SourceArg != tt.wantArg {
				t.Errorf("SourceArg = %q, want %q", row.SourceArg, tt.wantArg)
			}
			if row.SourceConflict != tt.wantConflict {
				t.Errorf("SourceConflict = %v, want %v", row.SourceConflict, tt.wantConflict)
			}
		})
	}
}

func TestBuildPluginGroupsCarriesLatestVersions(t *testing.T) {
	perProfile := []claudecli.PluginData{{
		Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true},
		},
		Marketplaces: []claudecli.Marketplace{githubMarket("m", "owner/m")},
	}}
	latest := map[claudecli.PluginID]string{id("p", "m"): "2.0.0"}

	groups := BuildPluginGroups(perProfile, latest)

	p := groups[0].Plugins[0]
	if p.LatestVersion != "2.0.0" || !p.Cells[0].Outdated {
		t.Errorf("got %+v, want latest 2.0.0 and outdated cell", p)
	}
}

func TestBuildPluginGroupsEmptyInput(t *testing.T) {
	if groups := BuildPluginGroups(nil, nil); len(groups) != 0 {
		t.Fatalf("got %d groups, want 0", len(groups))
	}
}
