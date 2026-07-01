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

var fooID = claudecli.PluginID{Name: "foo", Marketplace: "mp"}

func installedFoo(enabled bool) claudecli.PluginData {
	return claudecli.PluginData{
		Installed: []claudecli.InstalledPlugin{{ID: fooID, Version: "1.0.0", Enabled: enabled}},
	}
}

// modelWithCells builds a loaded model with one profile per PluginData; the
// profiles are named p0, p1, … with paths /h/p0, /h/p1, …
func modelWithCells(t *testing.T, runner *claudecli.FakeRunner, perProfile ...claudecli.PluginData) Model {
	t.Helper()
	profiles := make([]config.Profile, len(perProfile))
	for i := range perProfile {
		profiles[i] = config.Profile{Path: fmt.Sprintf("/h/p%d", i), Label: fmt.Sprintf("p%d", i)}
	}
	m := New(runner, profiles)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = resized.(Model)
	for i, data := range perProfile {
		loaded, _ := m.Update(profileLoadedMsg{index: i, plugins: data})
		m = loaded.(Model)
	}
	return m
}

func press(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	// Special keys must be sent as their real key types: a KeyRunes message
	// with the key's *name* as runes is not something a terminal produces.
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

// pluginCalls filters the fake's recorded calls down to `plugin <verb> <id>`
// action invocations, excluding the list/auth reads issued by refreshes.
func pluginCalls(runner *claudecli.FakeRunner, verb string) []claudecli.FakeCall {
	var calls []claudecli.FakeCall
	for _, c := range runner.Calls {
		if len(c.Args) == 3 && c.Args[0] == "plugin" && c.Args[1] == verb {
			calls = append(calls, c)
		}
	}
	return calls
}

func TestActionKeysInvokeCorrectCLI(t *testing.T) {
	tests := []struct {
		name string
		key  string
		verb string
		data []claudecli.PluginData
		col  int
	}{
		{"enable disabled plugin", "e", "enable", []claudecli.PluginData{installedFoo(false)}, 0},
		{"disable enabled plugin", "d", "disable", []claudecli.PluginData{installedFoo(true)}, 0},
		{"update installed plugin", "u", "update", []claudecli.PluginData{installedFoo(true)}, 0},
		// The target profile carries foo in its catalog: install requires the
		// plugin's marketplace configured there.
		{"install where absent", "i", "install", []claudecli.PluginData{
			installedFoo(true),
			{Available: []claudecli.AvailablePlugin{{ID: fooID, LatestVersion: "1.2.0"}}},
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			m := modelWithCells(t, runner, tt.data...)
			for range tt.col {
				m, _ = press(t, m, "right")
			}

			_, cmd := press(t, m, tt.key)
			if cmd == nil {
				t.Fatalf("key %q produced no command", tt.key)
			}
			msg, ok := cmd().(actionDoneMsg)
			if !ok {
				t.Fatalf("command produced %T, want actionDoneMsg", cmd())
			}
			if msg.err != nil {
				t.Fatalf("action failed: %v", msg.err)
			}

			calls := pluginCalls(runner, tt.verb)
			if len(calls) != 1 {
				t.Fatalf("got %d %q calls, want 1 (all calls: %v)", len(calls), tt.verb, runner.Calls)
			}
			wantDir := fmt.Sprintf("/h/p%d", tt.col)
			if calls[0].ProfileDir != wantDir {
				t.Errorf("profile dir = %q, want %q", calls[0].ProfileDir, wantDir)
			}
			wantArgs := []string{"plugin", tt.verb, "foo@mp"}
			if !slices.Equal(calls[0].Args, wantArgs) {
				t.Errorf("args = %v, want %v", calls[0].Args, wantArgs)
			}
		})
	}
}

func TestUninstallBlockedUntilConfirmed(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	m, cmd := press(t, m, "x")
	if cmd != nil {
		t.Fatal("uninstall ran before confirmation")
	}
	if len(pluginCalls(runner, "uninstall")) != 0 {
		t.Fatal("uninstall CLI call recorded before confirmation")
	}
	if view := m.View(); !strings.Contains(view, "uninstall foo@mp from p0? y/n") {
		t.Errorf("confirmation prompt missing:\n%s", view)
	}

	// Any key but y cancels.
	m, cmd = press(t, m, "n")
	if cmd != nil || len(pluginCalls(runner, "uninstall")) != 0 {
		t.Fatal("cancelled uninstall still ran")
	}
	if view := m.View(); strings.Contains(view, "y/n") {
		t.Errorf("prompt still shown after cancel:\n%s", view)
	}

	m, _ = press(t, m, "x")
	_, cmd = press(t, m, "y")
	if cmd == nil {
		t.Fatal("confirmed uninstall produced no command")
	}
	cmd()
	calls := pluginCalls(runner, "uninstall")
	if len(calls) != 1 {
		t.Fatalf("got %d uninstall calls after confirm, want 1", len(calls))
	}
	if !slices.Equal(calls[0].Args, []string{"plugin", "uninstall", "foo@mp"}) {
		t.Errorf("args = %v", calls[0].Args)
	}
}

func TestActionFailureSurfacesErrorAndKeepsState(t *testing.T) {
	runner := &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			"plugin update foo@mp": {Err: errors.New("boom")},
		},
	}
	m := modelWithCells(t, runner, installedFoo(true))

	m, cmd := press(t, m, "u")
	updated, refresh := m.Update(cmd())
	got := updated.(Model)

	if refresh != nil {
		t.Error("failed action triggered a refresh")
	}
	if got.columns[0].status != statusLoaded {
		t.Errorf("column status = %v, want loaded (unchanged)", got.columns[0].status)
	}
	view := got.View()
	if !strings.Contains(view, "boom") {
		t.Errorf("error missing from status line:\n%s", view)
	}
	if !strings.Contains(view, "v1.0.0") {
		t.Errorf("plugin data lost after failed action:\n%s", view)
	}
}

func TestActionTimeoutRefreshesProfile(t *testing.T) {
	// A timed-out action was killed mid-flight: its write may have (partially)
	// applied, so the column must reload instead of trusting its old data.
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	updated, refresh := m.Update(actionDoneMsg{
		index: 0, verb: "update", plugin: fooID,
		err: errors.New("signal: killed"), uncertain: true,
	})
	got := updated.(Model)

	if refresh == nil {
		t.Fatal("timed-out action triggered no refresh")
	}
	if got.columns[0].status != statusLoading {
		t.Errorf("column status = %v, want loading during refresh", got.columns[0].status)
	}
	if view := got.View(); !strings.Contains(view, "failed") {
		t.Errorf("failure status missing:\n%s", view)
	}
}

func TestActionSuccessRefreshesProfile(t *testing.T) {
	runner := &claudecli.FakeRunner{Responses: map[string]claudecli.FakeResponse{}}
	m := modelWithCells(t, runner, installedFoo(true), installedFoo(true))

	m, cmd := press(t, m, "d")
	// The CLI now reports the plugin disabled; the post-action refresh must
	// pick this up.
	runner.Responses["plugin list --available --json"] = claudecli.FakeResponse{
		Stdout: []byte(`{"installed":[{"id":"foo@mp","version":"1.0.0","enabled":false}],"available":[]}`),
	}
	updated, refresh := m.Update(cmd())
	got := updated.(Model)

	if got.columns[0].status != statusLoading {
		t.Errorf("acted-on column status = %v, want loading during refresh", got.columns[0].status)
	}
	if got.columns[1].status != statusLoaded {
		t.Errorf("other column status = %v, want loaded (untouched)", got.columns[1].status)
	}

	var loaded *profileLoadedMsg
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(profileLoadedMsg); ok {
			loaded = &l
		}
	}
	if loaded == nil {
		t.Fatal("refresh produced no profileLoadedMsg")
	}
	if loaded.index != 0 {
		t.Fatalf("refresh loaded profile %d, want 0", loaded.index)
	}

	final, _ := got.Update(*loaded)
	view := final.(Model).View()
	if !strings.Contains(view, "disabled (v1.0.0)") {
		t.Errorf("refreshed cell not updated to disabled:\n%s", view)
	}
}

func TestActionRefreshSkipsMarketplaceUpdateAndKeepsStaleFlag(t *testing.T) {
	// The catalog was refreshed moments earlier by the initial load: the
	// post-action refresh must not pay another network round-trip, and the
	// previous refresh outcome (stale or not) must carry forward.
	runner := &claudecli.FakeRunner{Responses: map[string]claudecli.FakeResponse{}}
	m := modelWithCells(t, runner, installedFoo(true))
	loaded, _ := m.Update(profileLoadedMsg{index: 0, plugins: installedFoo(true),
		latest: claudecli.LatestVersions{Stale: true}})
	m = loaded.(Model)

	m, cmd := press(t, m, "d")
	runner.Responses["plugin list --available --json"] = claudecli.FakeResponse{
		Stdout: []byte(`{"installed":[{"id":"foo@mp","version":"1.0.0","enabled":false}],"available":[]}`),
	}
	_, refresh := m.Update(cmd())

	var loadedMsg *profileLoadedMsg
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(profileLoadedMsg); ok {
			loadedMsg = &l
		}
	}
	if loadedMsg == nil {
		t.Fatal("refresh produced no profileLoadedMsg")
	}
	if !loadedMsg.latest.Stale {
		t.Error("refresh dropped the stale flag, want it carried forward")
	}
	for _, c := range runner.Calls {
		if strings.Join(c.Args, " ") == "plugin marketplace update" {
			t.Fatal("post-action refresh re-ran the marketplace update")
		}
	}
}

func TestSecondActionBlockedWhileActionInFlight(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	m, cmd := press(t, m, "u")
	if cmd == nil {
		t.Fatal("first action produced no command")
	}
	// The first action has not completed (its command was not run): a second
	// mutating action against the same profile must be rejected.
	m, cmd2 := press(t, m, "d")
	if cmd2 != nil {
		t.Fatal("second action ran while the first was still in flight")
	}
	if view := m.View(); !strings.Contains(view, "action in progress") {
		t.Errorf("busy hint missing:\n%s", view)
	}

	// Completing the first action clears the guard.
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.columns[0].busy {
		t.Error("column still busy after the action completed")
	}
}

func TestReloadSkipsBusyColumn(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true), installedFoo(true))

	// Start an update on p0; its command is not executed, so the action is
	// still in flight when reload is pressed.
	m, cmd := press(t, m, "u")
	if cmd == nil {
		t.Fatal("update produced no command")
	}
	if !m.columns[0].busy {
		t.Fatal("column 0 not busy after starting the action")
	}

	// Reload must not fire a fresh load for the busy column: its marketplace
	// refresh writes to the config dir the in-flight action is mutating.
	m, _ = press(t, m, "r")
	if m.columns[0].status != statusLoaded {
		t.Errorf("busy column status = %v, want statusLoaded (reload must skip it)", m.columns[0].status)
	}
	if m.columns[0].gen != 0 {
		t.Errorf("busy column gen = %d, want 0 (no load fired)", m.columns[0].gen)
	}
	if m.columns[1].status != statusLoading {
		t.Errorf("idle column status = %v, want statusLoading", m.columns[1].status)
	}
	if m.columns[1].gen != 1 {
		t.Errorf("idle column gen = %d, want 1", m.columns[1].gen)
	}
}

func TestSupersededLoadResultDropped(t *testing.T) {
	m := modelWithCells(t, &claudecli.FakeRunner{}, installedFoo(true))

	// Reload supersedes the initial generation; a result stamped with the old
	// generation (e.g. a slow pre-action load) must not flip the column back
	// to loaded with stale data.
	m, _ = press(t, m, "r")
	updated, _ := m.Update(profileLoadedMsg{index: 0, gen: 0, plugins: installedFoo(false)})
	got := updated.(Model)

	if got.columns[0].status != statusLoading {
		t.Errorf("column status = %v, want loading (stale result dropped)", got.columns[0].status)
	}
	if len(got.columns[0].plugins.Installed) != 1 || !got.columns[0].plugins.Installed[0].Enabled {
		t.Errorf("stale result overwrote column data: %+v", got.columns[0].plugins)
	}

	// The current generation's result still lands.
	updated, _ = got.Update(profileLoadedMsg{index: 0, gen: got.columns[0].gen,
		plugins: installedFoo(false)})
	got = updated.(Model)
	if got.columns[0].status != statusLoaded {
		t.Errorf("column status = %v, want loaded", got.columns[0].status)
	}
}

func TestActionOnWrongCellStateShowsHint(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	// Enable is only valid on a disabled plugin.
	m, cmd := press(t, m, "e")
	if cmd != nil {
		t.Fatal("invalid action still produced a command")
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("invalid action reached the CLI: %v", runner.Calls)
	}
	if view := m.View(); !strings.Contains(view, "cannot enable foo@mp in p0") {
		t.Errorf("hint missing:\n%s", view)
	}
}

func TestActionOnLoadingColumnShowsHint(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))
	// Add a second, still-loading profile and select it.
	m.columns = append(m.columns, column{profile: config.Profile{Path: "/h/p1", Label: "p1"}})
	m, _ = press(t, m, "right")

	m, cmd := press(t, m, "u")
	if cmd != nil || len(runner.Calls) != 0 {
		t.Fatal("action against a loading column ran")
	}
	if view := m.View(); !strings.Contains(view, "p1 is not loaded yet") {
		t.Errorf("hint missing:\n%s", view)
	}
}

func TestInstallBlockedWhenMarketplaceMissingInTarget(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	// p1 has no catalog entry for foo@mp: its marketplace is not configured.
	m := modelWithCells(t, runner, installedFoo(true), claudecli.PluginData{})
	m, _ = press(t, m, "right")

	m, cmd := press(t, m, "i")
	if cmd != nil || len(runner.Calls) != 0 {
		t.Fatal("install without the marketplace reached the CLI")
	}
	if view := m.View(); !strings.Contains(view, `marketplace "mp" is not configured`) {
		t.Errorf("missing-marketplace hint missing:\n%s", view)
	}
}

func TestPluginActionRefusesFlagLikeName(t *testing.T) {
	evil := claudecli.PluginID{Name: "--evil", Marketplace: "mp"}
	data := claudecli.PluginData{
		Installed: []claudecli.InstalledPlugin{{ID: evil, Version: "1.0.0", Enabled: true}},
	}
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, data)

	m, cmd := press(t, m, "u")
	if cmd != nil || len(runner.Calls) != 0 {
		t.Fatal("flag-like plugin name reached the CLI")
	}
	if view := m.View(); !strings.Contains(view, "looks like a CLI flag") {
		t.Errorf("refusal hint missing:\n%s", view)
	}
}

func TestActionKeysOnEmptyMatrixDoNothing(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, claudecli.PluginData{})

	for _, key := range []string{"e", "d", "u", "x", "i"} {
		if _, cmd := press(t, m, key); cmd != nil {
			t.Errorf("key %q on an empty matrix produced a command", key)
		}
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("empty-matrix action reached the CLI: %v", runner.Calls)
	}
}

func TestCtrlCQuitsDuringConfirmation(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	m, _ = press(t, m, "x") // arm the uninstall confirmation
	_, cmd := press(t, m, "ctrl+c")
	if cmd == nil {
		t.Fatal("ctrl+c during confirmation returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c during confirmation did not quit")
	}
	if len(pluginCalls(runner, "uninstall")) != 0 {
		t.Fatal("ctrl+c during confirmation ran the pending action")
	}
}

func TestTabSwitchClampsRowSelection(t *testing.T) {
	// Five plugin rows, one MCP row: switching tabs with a high selRow must
	// clamp it so `up` responds immediately on the new tab.
	installed := make([]claudecli.InstalledPlugin, 5)
	for i := range installed {
		installed[i] = claudecli.InstalledPlugin{
			ID:      claudecli.PluginID{Name: fmt.Sprintf("plug%d", i), Marketplace: "mp"},
			Version: "1.0.0", Enabled: true,
		}
	}
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, claudecli.PluginData{Installed: installed})
	for range 4 {
		m, _ = press(t, m, "down")
	}
	if m.selRow != 4 {
		t.Fatalf("selRow = %d, want 4", m.selRow)
	}

	m, _ = press(t, m, "tab")
	loaded, _ := m.Update(mcpLoadedMsg{index: 0, gen: m.columns[0].mcpGen,
		servers: []claudecli.MCPServer{{Name: "exa", Target: "url"}}})
	m = loaded.(Model)
	if m.selRow != 0 {
		t.Errorf("selRow after switch to a 1-row tab = %d, want 0 (clamped)", m.selRow)
	}
	if m, _ = press(t, m, "up"); m.selRow != 0 {
		t.Errorf("selRow after up = %d, want 0", m.selRow)
	}
}

func TestShiftTabSwitchesTabAndStartsMCPLoad(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	m, cmd := press(t, m, "shift+tab")
	if m.tab != tabMCP {
		t.Fatalf("tab after shift+tab = %v, want MCP", m.tab)
	}
	if cmd == nil {
		t.Fatal("first switch to the MCP tab fired no lazy load")
	}
	if m, _ = press(t, m, "shift+tab"); m.tab != tabPlugins {
		t.Errorf("tab after second shift+tab = %v, want plugins", m.tab)
	}
}

func TestActionKeysIgnoredOnMCPTab(t *testing.T) {
	for _, key := range []string{"e", "d", "u"} {
		t.Run(key, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			m := modelWithCells(t, runner, installedFoo(true))

			m, _ = press(t, m, "tab")
			_, cmd := press(t, m, key)
			if cmd != nil || len(runner.Calls) != 0 {
				t.Fatalf("action key %q acted on the MCP tab", key)
			}
		})
	}
}

func TestRowSelectionMovesAndClamps(t *testing.T) {
	data := claudecli.PluginData{
		Installed: []claudecli.InstalledPlugin{
			{ID: claudecli.PluginID{Name: "bar", Marketplace: "mp"}, Version: "2.0.0", Enabled: true},
			{ID: fooID, Version: "1.0.0", Enabled: true},
		},
	}
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, data)

	m, _ = press(t, m, "up")
	if m.selRow != 0 {
		t.Errorf("selRow after up at top = %d, want 0", m.selRow)
	}
	for range 3 {
		m, _ = press(t, m, "down")
	}
	if m.selRow != 1 {
		t.Errorf("selRow after 3 downs over 2 rows = %d, want 1 (clamped)", m.selRow)
	}

	// The action targets the selected row: rows sort bar@mp, foo@mp.
	_, cmd := press(t, m, "d")
	if cmd == nil {
		t.Fatal("disable on selected row produced no command")
	}
	cmd()
	calls := pluginCalls(runner, "disable")
	if len(calls) != 1 || !slices.Equal(calls[0].Args, []string{"plugin", "disable", "foo@mp"}) {
		t.Errorf("disable did not target selected row foo@mp: %v", runner.Calls)
	}
}
