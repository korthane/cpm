package model

import (
	"slices"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
)

func id(name, marketplace string) claudecli.PluginID {
	return claudecli.PluginID{Name: name, Marketplace: marketplace}
}

func TestBuildPluginMatrixUnionAndOrdering(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("zeta", "alpha-market"), Version: "1.0.0", Enabled: true},
			{ID: id("tool", "beta-market"), Version: "2.0.0", Enabled: true},
		}},
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("adder", "alpha-market"), Version: "0.1.0", Enabled: true},
			{ID: id("tool", "beta-market"), Version: "2.0.0", Enabled: true},
		}},
	}

	rows := BuildPluginMatrix(perProfile, nil)

	want := []claudecli.PluginID{
		id("adder", "alpha-market"),
		id("zeta", "alpha-market"),
		id("tool", "beta-market"),
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].ID != w {
			t.Errorf("row %d: got %v, want %v", i, rows[i].ID, w)
		}
		if len(rows[i].Cells) != len(perProfile) {
			t.Errorf("row %d: got %d cells, want %d", i, len(rows[i].Cells), len(perProfile))
		}
	}
}

func TestBuildPluginMatrixCellStates(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.2.3", Enabled: true},
		}},
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: false},
		}},
		{}, // profile without the plugin
	}

	rows := BuildPluginMatrix(perProfile, nil)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	cells := rows[0].Cells
	wantCells := []PluginCell{
		{State: Installed, Version: "1.2.3"},
		{State: Disabled, Version: "1.0.0"},
		{State: Absent},
	}
	for i, w := range wantCells {
		if cells[i] != w {
			t.Errorf("cell %d: got %+v, want %+v", i, cells[i], w)
		}
	}
}

func TestBuildPluginMatrixLatestVersionAndOutdated(t *testing.T) {
	tests := []struct {
		name         string
		installed    string
		latest       string
		wantOutdated bool
	}{
		{"behind latest", "1.0.0", "1.2.0", true},
		{"equal", "1.2.0", "1.2.0", false},
		{"equal modulo v prefix", "1.5.5", "v1.5.5", false},
		{"behind latest with v prefix", "1.5.4", "v1.5.5", true},
		{"ahead of latest", "2.0.0", "1.2.0", false},
		{"no latest known", "1.0.0", "", false},
		{"unknown installed version", "", "1.2.0", false},
		{"numeric segment compare", "1.9.0", "1.10.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perProfile := []claudecli.PluginData{
				{Installed: []claudecli.InstalledPlugin{
					{ID: id("p", "m"), Version: tt.installed, Enabled: true},
				}},
			}
			latest := map[claudecli.PluginID]string{id("p", "m"): tt.latest}

			rows := BuildPluginMatrix(perProfile, latest)

			if len(rows) != 1 {
				t.Fatalf("got %d rows, want 1", len(rows))
			}
			if rows[0].LatestVersion != tt.latest {
				t.Errorf("LatestVersion: got %q, want %q", rows[0].LatestVersion, tt.latest)
			}
			if got := rows[0].Cells[0].Outdated; got != tt.wantOutdated {
				t.Errorf("Outdated: got %v, want %v", got, tt.wantOutdated)
			}
		})
	}
}

func TestBuildPluginMatrixDisabledCellCanBeOutdated(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: false},
		}},
	}
	latest := map[claudecli.PluginID]string{id("p", "m"): "2.0.0"}

	rows := BuildPluginMatrix(perProfile, latest)

	cell := rows[0].Cells[0]
	if cell.State != Disabled || !cell.Outdated {
		t.Errorf("got %+v, want disabled and outdated", cell)
	}
}

func TestBuildPluginMatrixSingleProfile(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("only", "m"), Version: "3.1.4", Enabled: true},
		}},
	}

	rows := BuildPluginMatrix(perProfile, nil)

	if len(rows) != 1 || len(rows[0].Cells) != 1 {
		t.Fatalf("got %d rows / %d cells, want 1/1", len(rows), len(rows[0].Cells))
	}
	if rows[0].LatestVersion != "" {
		t.Errorf("LatestVersion: got %q, want empty", rows[0].LatestVersion)
	}
}

func TestBuildPluginMatrixNoInstalledPlugins(t *testing.T) {
	// Available-only catalog entries must not create rows: the matrix lists
	// plugins seen in at least one profile.
	perProfile := []claudecli.PluginData{
		{Available: []claudecli.AvailablePlugin{
			{ID: id("catalog-only", "m"), LatestVersion: "9.9.9"},
		}},
		{},
	}

	if rows := BuildPluginMatrix(perProfile, nil); len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

func TestBuildPluginMatrixEmptyInput(t *testing.T) {
	if rows := BuildPluginMatrix(nil, nil); len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

func TestMergeLatestVersionsUnionAcrossProfiles(t *testing.T) {
	perProfile := []claudecli.LatestVersions{
		{Versions: map[claudecli.PluginID]string{id("a", "m"): "1.0.0"}},
		{Versions: map[claudecli.PluginID]string{id("b", "m"): "2.0.0"}},
	}

	got, stale := MergeLatestVersions(perProfile)

	if stale {
		t.Error("stale = true, want false when no profile is stale")
	}
	want := map[claudecli.PluginID]string{
		id("a", "m"): "1.0.0",
		id("b", "m"): "2.0.0",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("latest[%v] = %q, want %q", k, got[k], v)
		}
	}
}

func TestMergeLatestVersionsNewestWinsOnDisagreement(t *testing.T) {
	// Profiles refresh their catalogs at different times, so the same plugin
	// can carry different latest versions; the newest one wins either way
	// round, including numeric (not lexical) segment ordering.
	perProfile := []claudecli.LatestVersions{
		{Versions: map[claudecli.PluginID]string{id("p", "m"): "1.10.0"}},
		{Versions: map[claudecli.PluginID]string{id("p", "m"): "1.9.0"}},
	}

	if got, _ := MergeLatestVersions(perProfile); got[id("p", "m")] != "1.10.0" {
		t.Errorf("latest = %q, want 1.10.0", got[id("p", "m")])
	}

	slices.Reverse(perProfile)
	if got, _ := MergeLatestVersions(perProfile); got[id("p", "m")] != "1.10.0" {
		t.Errorf("latest after reverse = %q, want 1.10.0", got[id("p", "m")])
	}
}

func TestMergeLatestVersionsIgnoresEmpty(t *testing.T) {
	perProfile := []claudecli.LatestVersions{
		{Versions: map[claudecli.PluginID]string{id("p", "m"): "1.0.0"}},
		{Versions: map[claudecli.PluginID]string{id("p", "m"): ""}},
	}

	if got, _ := MergeLatestVersions(perProfile); got[id("p", "m")] != "1.0.0" {
		t.Errorf("latest = %q, want 1.0.0 (empty must not overwrite)", got[id("p", "m")])
	}
	if got, _ := MergeLatestVersions(nil); len(got) != 0 {
		t.Errorf("MergeLatestVersions(nil) = %v, want empty", got)
	}
}

func TestMergeLatestVersionsStaleWhenAnyProfileStale(t *testing.T) {
	perProfile := []claudecli.LatestVersions{
		{Versions: map[claudecli.PluginID]string{id("p", "m"): "1.0.0"}},
		{Stale: true},
	}

	if _, stale := MergeLatestVersions(perProfile); !stale {
		t.Error("stale = false, want true when one profile's refresh failed")
	}
}
