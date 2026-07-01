package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/model"
)

func TestFormatPluginCell(t *testing.T) {
	tests := []struct {
		name string
		cell model.PluginCell
		want string
	}{
		{"absent", model.PluginCell{State: model.Absent}, "—"},
		{"installed", model.PluginCell{State: model.Installed, Version: "1.2.3"}, "v1.2.3"},
		{"installed v-prefixed", model.PluginCell{State: model.Installed, Version: "v1.2.3"}, "v1.2.3"},
		{"installed unknown version", model.PluginCell{State: model.Installed}, "installed"},
		{"installed outdated", model.PluginCell{State: model.Installed, Version: "1.0.0", Outdated: true}, "v1.0.0 ↑"},
		{"disabled", model.PluginCell{State: model.Disabled, Version: "2.1.0"}, "disabled (v2.1.0)"},
		{"disabled unknown version", model.PluginCell{State: model.Disabled}, "disabled"},
		{"disabled outdated", model.PluginCell{State: model.Disabled, Version: "2.1.0", Outdated: true}, "disabled (v2.1.0) ↑"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPluginCell(tt.cell); got != tt.want {
				t.Errorf("formatPluginCell(%+v) = %q, want %q", tt.cell, got, tt.want)
			}
		})
	}
}

// fourProfiles builds a loaded model with 4 profiles in a terminal narrow
// enough that only one profile column fits next to the pinned column.
func fourProfiles(t *testing.T) Model {
	t.Helper()
	profiles := []config.Profile{
		{Path: "/h/a", Label: "alpha"},
		{Path: "/h/b", Label: "beta"},
		{Path: "/h/c", Label: "gamma"},
		{Path: "/h/d", Label: "delta"},
	}
	m := New(okRunner(), profiles)

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = resized.(Model)

	pid := claudecli.PluginID{Name: "foo", Marketplace: "mp"}
	data := claudecli.PluginData{
		Installed: []claudecli.InstalledPlugin{
			{ID: pid, Version: "1.0.0", Enabled: true},
		},
	}
	latest := claudecli.LatestVersions{Versions: map[claudecli.PluginID]string{pid: "1.2.0"}}
	for i := range profiles {
		loaded, _ := m.Update(profileLoadedMsg{
			index:   i,
			auth:    claudecli.AuthStatus{Email: "u@example.com", SubscriptionType: "pro"},
			plugins: data,
			latest:  latest,
		})
		m = loaded.(Model)
	}
	return m
}

func scrollRight(t *testing.T, m Model, times int) Model {
	t.Helper()
	for range times {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = updated.(Model)
	}
	return m
}

func TestPinnedColumnVisibleAtAllScrollOffsets(t *testing.T) {
	m := fourProfiles(t)

	for offset := range 4 {
		view := scrollRight(t, m, offset).View()
		if !strings.Contains(view, "plugin@marketplace") {
			t.Errorf("offset %d: pinned header missing:\n%s", offset, view)
		}
		if !strings.Contains(view, "foo@mp") {
			t.Errorf("offset %d: pinned identity cell missing:\n%s", offset, view)
		}
		if !strings.Contains(view, "latest") {
			t.Errorf("offset %d: pinned 'latest' header missing:\n%s", offset, view)
		}
		if !strings.Contains(view, "v1.2.0") {
			t.Errorf("offset %d: latest version missing from pinned column:\n%s", offset, view)
		}
	}
}

func TestScrollChangesVisibleProfileColumns(t *testing.T) {
	m := fourProfiles(t)
	labels := []string{"alpha", "beta", "gamma", "delta"}

	for offset, label := range labels {
		view := scrollRight(t, m, offset).View()
		if !strings.Contains(view, label) {
			t.Errorf("offset %d: visible column %q missing:\n%s", offset, label, view)
		}
		for other, otherLabel := range labels {
			if other != offset && strings.Contains(view, otherLabel) {
				t.Errorf("offset %d: hidden column %q rendered:\n%s", offset, otherLabel, view)
			}
		}
	}
}

func TestScrollClampsAtBothEnds(t *testing.T) {
	m := fourProfiles(t)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := updated.(Model).selCol; got != 0 {
		t.Errorf("left at offset 0: selCol = %d, want 0", got)
	}

	m = scrollRight(t, m, 10)
	if m.selCol != 3 {
		t.Errorf("selCol after 10 rights = %d, want 3 (clamped)", m.selCol)
	}
}

func TestScrollIndicators(t *testing.T) {
	m := fourProfiles(t)

	view := m.View()
	if strings.Contains(view, "◀") {
		t.Errorf("offset 0: left indicator shown:\n%s", view)
	}
	if !strings.Contains(view, "▶") {
		t.Errorf("offset 0: right indicator missing:\n%s", view)
	}

	view = scrollRight(t, m, 2).View()
	if !strings.Contains(view, "◀") || !strings.Contains(view, "▶") {
		t.Errorf("middle offset: want both indicators:\n%s", view)
	}

	view = scrollRight(t, m, 3).View()
	if !strings.Contains(view, "◀") {
		t.Errorf("last offset: left indicator missing:\n%s", view)
	}
	if strings.Contains(view, "▶") {
		t.Errorf("last offset: right indicator shown:\n%s", view)
	}
}

func TestHeaderShowsProfileEmailAndPlan(t *testing.T) {
	m := fourProfiles(t)
	view := m.View()

	for _, want := range []string{"alpha", "/h/a", "u@example.com · pro"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing header part %q:\n%s", want, view)
		}
	}
}

func TestBodyCellFormattingInView(t *testing.T) {
	profiles := []config.Profile{
		{Path: "/h/a", Label: "alpha"},
		{Path: "/h/b", Label: "beta"},
		{Path: "/h/c", Label: "gamma"},
	}
	m := New(okRunner(), profiles)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = resized.(Model)

	pid := claudecli.PluginID{Name: "foo", Marketplace: "mp"}
	latest := claudecli.LatestVersions{Versions: map[claudecli.PluginID]string{pid: "1.2.0"}}
	perProfile := []claudecli.PluginData{
		{Installed: []claudecli.InstalledPlugin{{ID: pid, Version: "1.0.0", Enabled: true}}},
		{Installed: []claudecli.InstalledPlugin{{ID: pid, Version: "1.2.0", Enabled: false}}},
		{}, // absent
	}
	for i, data := range perProfile {
		loaded, _ := m.Update(profileLoadedMsg{index: i, plugins: data, latest: latest})
		m = loaded.(Model)
	}

	view := m.View()
	for _, want := range []string{"v1.0.0 ↑", "disabled (v1.2.0)", "—", "not logged in"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q:\n%s", want, view)
		}
	}
}

func TestLoadingColumnShowsSpinnerInTable(t *testing.T) {
	m := New(okRunner(), testProfiles)
	loaded, _ := m.Update(profileLoadedMsg{index: 0})
	m = loaded.(Model)

	view := m.View()
	if !strings.Contains(view, "loading…") {
		t.Errorf("View() missing loading marker for pending column:\n%s", view)
	}
	if !strings.Contains(view, m.columns[1].spinner.View()) {
		t.Errorf("View() missing spinner frame for pending column:\n%s", view)
	}
}
