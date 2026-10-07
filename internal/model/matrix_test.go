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

func TestBuildPluginMatrixCarriesScope(t *testing.T) {
	// The UI refuses actions on non-user scopes, so the per-profile scope must
	// survive aggregation into the cell.
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "project"},
		}},
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "user"},
		}},
	}

	rows := BuildPluginMatrix(perProfile, nil)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := rows[0].Cells[0].Scope; got != "project" {
		t.Errorf("cell 0 scope = %q, want project", got)
	}
	if got := rows[0].Cells[1].Scope; got != "user" {
		t.Errorf("cell 1 scope = %q, want user", got)
	}
}

func TestBuildPluginMatrixSameProfileScopeCollisionPrefersUser(t *testing.T) {
	// A profile can report one plugin installed at two scopes at once. Only
	// "user" is actionable via cpm's --scope user-pinned CLI calls, so it must
	// win regardless of CLI output order instead of last-write-wins silently
	// dropping the actionable entry.
	projectFirst := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "project"},
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "user"},
		}},
	}
	userFirst := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "user"},
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "project"},
		}},
	}

	for name, perProfile := range map[string][]claudecli.PluginData{
		"project-first": projectFirst,
		"user-first":    userFirst,
	} {
		rows := BuildPluginMatrix(perProfile, nil)
		if len(rows) != 1 {
			t.Fatalf("%s: got %d rows, want 1", name, len(rows))
		}
		if got := rows[0].Cells[0].Scope; got != "user" {
			t.Errorf("%s: cell scope = %q, want user", name, got)
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
		// Four-segment refs exist in the wild; they are the reason for the
		// custom compare — a semver library would reject them.
		{"four segments behind", "1.2.3.4", "1.2.3.5", true},
		{"four segments equal", "1.2.3.4", "1.2.3.4", false},
		{"missing patch segment equals zero", "1.2", "1.2.0", false},
		{"missing patch segment behind", "1.2", "1.2.1", true},
		{"pre-release behind its release", "1.0.0-rc1", "1.0.0", true},
		{"release not behind its pre-release", "1.0.0", "1.0.0-rc1", false},
		{"pre-releases ordered lexically", "1.0.0-rc1", "1.0.0-rc2", true},
		{"non-numeric segments compared lexically", "1.2.x", "1.2.y", true},
		{"fully non-numeric never outdated when equal", "beta", "beta", false},
		{"fully non-numeric compared lexically", "alpha", "beta", true},
		{"commit-hash installed version is unknown", "0a1b2c3d", "1.2.0", false},
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

func TestIsOutdated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		installed, latest string
		want              bool
	}{
		{"0.34.0", "0.35.1", true},
		{"0.35.1", "0.35.1", false},
		{"1.5.5", "v1.5.6", true},
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		// Commit-hash installs have no order against a release version.
		{"0a1b2c3d4e5f", "1.2.0", false},
		{"1a2b3c", "1.2.0", false},
		{"9f8e7d6c5b4a", "10.0.0", false},
		{"1.0.0", "abc1234", false},
		{"alpha", "beta", true},
	}
	for _, tt := range tests {
		if got := IsOutdated(tt.installed, tt.latest); got != tt.want {
			t.Errorf("IsOutdated(%q, %q) = %v, want %v",
				tt.installed, tt.latest, got, tt.want)
		}
	}
}

func TestComparePluginIDsOrdersByMarketplaceThenName(t *testing.T) {
	t.Parallel()
	ids := []claudecli.PluginID{
		{Name: "b", Marketplace: "z"},
		{Name: "c", Marketplace: "a"},
		{Name: "a", Marketplace: "z"},
	}
	slices.SortFunc(ids, ComparePluginIDs)
	want := []claudecli.PluginID{
		{Name: "c", Marketplace: "a"},
		{Name: "a", Marketplace: "z"},
		{Name: "b", Marketplace: "z"},
	}
	if !slices.Equal(ids, want) {
		t.Errorf("sorted = %v, want %v", ids, want)
	}
}

func TestMergeLatestVersionsIgnoresCommitHashes(t *testing.T) {
	// A hash sorts lexically above any release, so letting it in would
	// displace a real version and mask the upgrade behind it.
	perProfile := []claudecli.LatestVersions{
		{Versions: map[claudecli.PluginID]string{
			id("p", "m"): "1.2.0", id("h", "m"): "abc1234",
		}},
		{Versions: map[claudecli.PluginID]string{id("p", "m"): "abc1234"}},
	}

	for range 2 {
		got, _ := MergeLatestVersions(perProfile)
		if got[id("p", "m")] != "1.2.0" {
			t.Errorf("latest = %q, want 1.2.0", got[id("p", "m")])
		}
		if v, ok := got[id("h", "m")]; ok {
			t.Errorf("hash-only latest = %q, want no entry", v)
		}
		slices.Reverse(perProfile)
	}

	merged, _ := MergeLatestVersions(perProfile)
	installed := []claudecli.InstalledPlugin{
		{ID: id("p", "m"), Version: "1.0.0", Enabled: true, Scope: "user"},
	}
	rows := BuildPluginMatrix(
		[]claudecli.PluginData{{Installed: installed}}, merged)
	if !rows[0].Cells[0].Outdated || rows[0].LatestVersion != "1.2.0" {
		t.Errorf("row = %+v, want outdated against 1.2.0", rows[0])
	}
}

func TestBuildPluginMatrixCarriesCommitSHA(t *testing.T) {
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true,
				Scope: "user", CommitSHA: "aaa111"},
		}},
		{}, // profile without the plugin
	}

	rows := BuildPluginMatrix(perProfile, nil)

	if got := rows[0].Cells[0].CommitSHA; got != "aaa111" {
		t.Errorf("CommitSHA = %q, want aaa111", got)
	}
	if got := rows[0].Cells[1].CommitSHA; got != "" {
		t.Errorf("absent cell CommitSHA = %q, want empty", got)
	}
}

func TestBuildPluginMatrixCommitSHAFollowsCellScope(t *testing.T) {
	// The user-scope install wins the cell, so its SHA must too.
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{
			{ID: id("p", "m"), Version: "1.0.0", Enabled: true,
				Scope: "user", CommitSHA: "user-sha"},
			{ID: id("p", "m"), Version: "2.0.0", Enabled: true,
				Scope: "project", CommitSHA: "project-sha"},
		}},
	}

	cell := BuildPluginMatrix(perProfile, nil)[0].Cells[0]

	if cell.Version != "1.0.0" || cell.CommitSHA != "user-sha" {
		t.Errorf("cell = %+v, want user install's version and SHA", cell)
	}
}

func TestLatestSource(t *testing.T) {
	p := id("p", "m")
	src := func(commit string) claudecli.PluginSource {
		return claudecli.PluginSource{RepoURL: "owner/repo", Commit: commit}
	}
	lv := func(version, commit string, stale bool) claudecli.LatestVersions {
		return claudecli.LatestVersions{
			Versions: map[claudecli.PluginID]string{p: version},
			Sources:  map[claudecli.PluginID]claudecli.PluginSource{p: src(commit)},
			Stale:    stale,
		}
	}
	// incomplete is a relative source whose git lookup failed: it has a
	// clone to read but no repo or commit for links.
	incomplete := func(stale bool) claudecli.LatestVersions {
		return claudecli.LatestVersions{
			Versions: map[claudecli.PluginID]string{p: "2.0.0"},
			Sources: map[claudecli.PluginID]claudecli.PluginSource{
				p: {Path: "plugins/p", CloneDir: "/clone"}},
			Stale: stale,
		}
	}

	tests := []struct {
		name       string
		perProfile []claudecli.LatestVersions
		latest     string
		want       string
		wantOK     bool
	}{
		{
			name: "source of the profile with the latest version",
			perProfile: []claudecli.LatestVersions{
				lv("1.0.0", "old", false), lv("2.0.0", "new", false),
			},
			latest: "2.0.0", want: "new", wantOK: true,
		},
		{
			name: "equal under version compare, not string equality",
			perProfile: []claudecli.LatestVersions{
				lv("v2.0", "new", false),
			},
			latest: "2.0.0", want: "new", wantOK: true,
		},
		{
			name: "tie prefers a non-stale profile",
			perProfile: []claudecli.LatestVersions{
				lv("2.0.0", "stale", true), lv("2.0.0", "fresh", false),
			},
			latest: "2.0.0", want: "fresh", wantOK: true,
		},
		{
			name: "tie among non-stale prefers profile order",
			perProfile: []claudecli.LatestVersions{
				lv("2.0.0", "first", false), lv("2.0.0", "second", false),
			},
			latest: "2.0.0", want: "first", wantOK: true,
		},
		{
			name: "only stale profiles still yield a source",
			perProfile: []claudecli.LatestVersions{
				lv("1.0.0", "old", false), lv("2.0.0", "stale", true),
			},
			latest: "2.0.0", want: "stale", wantOK: true,
		},
		{
			name: "tie prefers a source that can build links",
			perProfile: []claudecli.LatestVersions{
				incomplete(false), lv("2.0.0", "second", false),
			},
			latest: "2.0.0", want: "second", wantOK: true,
		},
		{
			name: "linkable stale source beats a fresh one without links",
			perProfile: []claudecli.LatestVersions{
				incomplete(false), lv("2.0.0", "stale", true),
			},
			latest: "2.0.0", want: "stale", wantOK: true,
		},
		{
			name: "source without links still beats none",
			perProfile: []claudecli.LatestVersions{
				incomplete(false),
			},
			latest: "2.0.0", want: "", wantOK: true,
		},
		{
			name: "profile without a source is skipped",
			perProfile: []claudecli.LatestVersions{
				{Versions: map[claudecli.PluginID]string{p: "2.0.0"}},
				lv("2.0.0", "second", false),
			},
			latest: "2.0.0", want: "second", wantOK: true,
		},
		{
			name: "no profile has the latest version",
			perProfile: []claudecli.LatestVersions{
				lv("1.0.0", "old", false),
			},
			latest: "2.0.0",
		},
		{
			name: "matching version without a source",
			perProfile: []claudecli.LatestVersions{{
				Versions: map[claudecli.PluginID]string{p: "2.0.0"},
			}},
			latest: "2.0.0",
		},
		{
			name: "empty latest",
			perProfile: []claudecli.LatestVersions{
				lv("", "x", false),
			},
			latest: "",
		},
		{
			name:   "no profiles",
			latest: "2.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := LatestSource(tt.perProfile, p, tt.latest)
			if ok != tt.wantOK || got.Commit != tt.want {
				t.Errorf("LatestSource = (%q, %v), want (%q, %v)",
					got.Commit, ok, tt.want, tt.wantOK)
			}
		})
	}
}
