package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
			auth:    claudecli.AuthStatus{LoggedIn: true, Email: "u@example.com", SubscriptionType: "pro"},
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
		if !strings.Contains(view, "marketplace / plugin") {
			t.Errorf("offset %d: pinned header missing:\n%s", offset, view)
		}
		if !strings.Contains(view, chevronUnfolded+" mp") || !strings.Contains(view, "  foo") {
			t.Errorf("offset %d: pinned identity cells missing:\n%s", offset, view)
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

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		w    int
		want string
	}{
		{"fits untouched", "ab", 2, "ab"},
		{"cut with ellipsis", "abcdef", 4, "abc…"},
		{"width one", "abc", 1, "…"},
		{"zero width", "abc", 0, ""},
		{"negative width", "abc", -1, ""},
		{"empty at zero width", "", 0, ""},
		{"wide runes fit", "日本語", 6, "日本語"},
		{"wide runes cut on cell boundary", "日本語", 4, "日…"},
		{"wide rune would straddle the cut", "日本語", 5, "日本…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.w)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.want)
			}
			if w := lipgloss.Width(got); w > max(0, tt.w) {
				t.Errorf("truncate(%q, %d) is %d cells wide, over the limit", tt.s, tt.w, w)
			}
		})
	}
}

func TestVerticalScrollKeepsSelectedRowVisible(t *testing.T) {
	installed := make([]claudecli.InstalledPlugin, 20)
	for i := range installed {
		installed[i] = claudecli.InstalledPlugin{
			ID:      claudecli.PluginID{Name: fmt.Sprintf("plug%02d", i), Marketplace: "mp"},
			Version: "1.0.0", Enabled: true,
		}
	}
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, claudecli.PluginData{Installed: installed})
	// Height 16 leaves room for 5 body rows next to the fixed chrome.
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = resized.(Model)

	// 21 visible rows: the mp group header plus 20 plugins.
	view := m.View()
	if !strings.Contains(view, "plug00") {
		t.Errorf("top window missing first row:\n%s", view)
	}
	if strings.Contains(view, "plug10") {
		t.Errorf("row beyond the window rendered:\n%s", view)
	}
	if !strings.Contains(view, "… rows 1–5 of 21") {
		t.Errorf("overflow marker missing:\n%s", view)
	}

	for range 11 {
		m, _ = press(t, m, "down")
	}
	view = m.View()
	if !strings.Contains(view, "plug10") {
		t.Errorf("selected row not scrolled into view:\n%s", view)
	}
	if strings.Contains(view, "plug00") {
		t.Errorf("row scrolled out still rendered:\n%s", view)
	}
	if !strings.Contains(view, "… rows 8–12 of 21") {
		t.Errorf("overflow marker not updated:\n%s", view)
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
	if !strings.Contains(view, m.spinner.View()) {
		t.Errorf("View() missing spinner frame for pending column:\n%s", view)
	}
}
