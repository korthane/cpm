package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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

// pluginCalls filters the fake's recorded calls down to
// `plugin <verb> --scope user <id>` action invocations, excluding the
// list/auth reads issued by refreshes.
func pluginCalls(runner *claudecli.FakeRunner, verb string) []claudecli.FakeCall {
	var calls []claudecli.FakeCall
	for _, c := range runner.Calls {
		if len(c.Args) == 5 && c.Args[0] == "plugin" && c.Args[1] == verb {
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
			raw := cmd()
			msg, ok := raw.(actionDoneMsg)
			if !ok {
				t.Fatalf("command produced %T, want actionDoneMsg", raw)
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
			wantArgs := []string{"plugin", tt.verb, "--scope", "user", "foo@mp"}
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
	if !slices.Equal(calls[0].Args, []string{"plugin", "uninstall", "--scope", "user", "foo@mp"}) {
		t.Errorf("args = %v", calls[0].Args)
	}
}

func TestActionFailureSurfacesErrorAndKeepsState(t *testing.T) {
	runner := &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			"plugin update --scope user foo@mp": {Err: errors.New("boom")},
		},
	}
	m := modelWithCells(t, runner, installedFoo(true))

	m, cmd := press(t, m, "u")
	if cmd == nil {
		t.Fatal("expected command")
	}
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

// ctxWaitRunner blocks until the command context expires and returns its
// error, simulating a hung claude killed by the timeout.
type ctxWaitRunner struct{}

func (ctxWaitRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestActionCommandsDeriveUncertainFromTimeout(t *testing.T) {
	// uncertain must come from the command's own expired context, not merely
	// from a failed CLI call — this drives the real derivation, unlike the
	// *TimeoutRefreshesProfile tests which inject the flag.
	old := cmdTimeout
	cmdTimeout = 10 * time.Millisecond
	t.Cleanup(func() { cmdTimeout = old })

	plugin, ok := runPluginAction(ctxWaitRunner{}, 0, "/h/p0", fooID, "update")().(actionDoneMsg)
	if !ok || plugin.err == nil || !plugin.uncertain {
		t.Errorf("timed-out plugin action = %+v, want err and uncertain", plugin)
	}
	mcp, ok := runMCPRemove(ctxWaitRunner{}, 0, "/h/p0", "exa")().(mcpActionDoneMsg)
	if !ok || mcp.err == nil || !mcp.uncertain {
		t.Errorf("timed-out MCP remove = %+v, want err and uncertain", mcp)
	}
}

func TestActionCommandsPlainFailureIsNotUncertain(t *testing.T) {
	// A CLI-reported failure changed nothing; flagging it uncertain would
	// trigger a spurious column reload after every failed action.
	runner := &claudecli.FakeRunner{Default: claudecli.FakeResponse{Err: errors.New("boom")}}

	plugin := runPluginAction(runner, 0, "/h/p0", fooID, "update")().(actionDoneMsg)
	if plugin.err == nil || plugin.uncertain {
		t.Errorf("failed plugin action = %+v, want err and not uncertain", plugin)
	}
	mcp := runMCPRemove(runner, 0, "/h/p0", "exa")().(mcpActionDoneMsg)
	if mcp.err == nil || mcp.uncertain {
		t.Errorf("failed MCP remove = %+v, want err and not uncertain", mcp)
	}
}

// deadlineCheckRunner records each call and whether its context carried a
// deadline.
type deadlineCheckRunner struct {
	calls   int
	missing []string
}

func (r *deadlineCheckRunner) Run(ctx context.Context, _ string, args ...string) ([]byte, error) {
	r.calls++
	if _, ok := ctx.Deadline(); !ok {
		r.missing = append(r.missing, strings.Join(args, " "))
	}
	return nil, nil
}

func TestEveryUIFiredCommandCarriesTimeout(t *testing.T) {
	// A hung claude must degrade to a per-column error; a command constructor
	// losing its context.WithTimeout would freeze that column forever.
	r := &deadlineCheckRunner{}
	loadProfile(r, 0, 1, "/h/p0")()
	refreshProfile(r, 0, 1, "/h/p0", false)()
	loadMCPProfile(r, 0, 1, "/h/p0")()
	runPluginAction(r, 0, "/h/p0", fooID, "update")()
	runMCPRemove(r, 0, "/h/p0", "exa")()

	if r.calls == 0 {
		t.Fatal("no CLI calls recorded")
	}
	if len(r.missing) != 0 {
		t.Errorf("CLI calls fired without a deadline: %v", r.missing)
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

func TestReloadSkipsLoadingColumn(t *testing.T) {
	// A loading column already has a `plugin marketplace update` (a write) in
	// flight; a second load would be a concurrent writer on the same config
	// dir, and the gen stamp cannot kill the in-flight process.
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))

	m, _ = press(t, m, "r")
	if m.columns[0].gen != 1 || m.columns[0].status != statusLoading {
		t.Fatalf("first reload: gen = %d, status = %v, want 1/loading",
			m.columns[0].gen, m.columns[0].status)
	}
	m, _ = press(t, m, "r")
	if m.columns[0].gen != 1 {
		t.Errorf("reload while loading fired a second load: gen = %d, want 1", m.columns[0].gen)
	}
}

func TestActionRefreshReloadsStartedMCPTab(t *testing.T) {
	// Plugin actions can add or remove plugin-provided MCP servers, so a
	// loaded MCP tab must reload the acted-on column too — otherwise it keeps
	// showing servers of an uninstalled plugin.
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, installedFoo(true))
	m, _ = press(t, m, "tab") // starts the lazy MCP load
	loaded, _ := m.Update(mcpLoadedMsg{index: 0, gen: m.columns[0].mcpGen,
		servers: []claudecli.MCPServer{{Name: "plugin:foo:srv", Target: "stdio"}}})
	m = loaded.(Model)
	m, _ = press(t, m, "tab") // back to plugins

	m, cmd := press(t, m, "u")
	updated, refresh := m.Update(cmd())
	got := updated.(Model)

	if got.columns[0].mcpStatus != statusLoading {
		t.Errorf("mcpStatus = %v, want loading after plugin action", got.columns[0].mcpStatus)
	}
	var mcpReloaded bool
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(mcpLoadedMsg); ok && l.gen == got.columns[0].mcpGen {
			mcpReloaded = true
		}
	}
	if !mcpReloaded {
		t.Error("post-action refresh fired no MCP reload for the acted column")
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

func TestSupersededErrResultDropped(t *testing.T) {
	m := modelWithCells(t, &claudecli.FakeRunner{}, installedFoo(true))

	// Reload supersedes the initial generation; an error stamped with the old
	// generation must not flip a reloading column into the error state.
	m, _ = press(t, m, "r")
	updated, _ := m.Update(profileErrMsg{index: 0, gen: 0, err: errors.New("stale boom")})
	got := updated.(Model)

	if got.columns[0].status != statusLoading {
		t.Errorf("column status = %v, want loading (stale error dropped)", got.columns[0].status)
	}
	if got.columns[0].err != nil {
		t.Errorf("stale error stored on the column: %v", got.columns[0].err)
	}
}

func TestUpClampsAfterRowSetShrinks(t *testing.T) {
	installed := make([]claudecli.InstalledPlugin, 5)
	for i := range installed {
		installed[i] = claudecli.InstalledPlugin{
			ID:      claudecli.PluginID{Name: fmt.Sprintf("plug%d", i), Marketplace: "mp"},
			Version: "1.0.0", Enabled: true,
		}
	}
	m := modelWithCells(t, &claudecli.FakeRunner{}, claudecli.PluginData{Installed: installed})
	for range 4 {
		m, _ = press(t, m, "down")
	}

	// A reload delivers a shrunken row set under the out-of-range selection;
	// `up` must clamp before moving, not walk back one dead press at a time.
	m, _ = press(t, m, "r")
	updated, _ := m.Update(profileLoadedMsg{index: 0, gen: m.columns[0].gen,
		plugins: claudecli.PluginData{Installed: installed[:2]}})
	m = updated.(Model)

	if m, _ = press(t, m, "up"); m.selRow != 0 {
		t.Errorf("selRow after up on a shrunken row set = %d, want 0 (clamped, then moved)", m.selRow)
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

func TestActionOnNonUserScopeShowsHint(t *testing.T) {
	// Actions pin --scope user; a project/local-scope install (cwd-dependent)
	// must be refused with a hint instead of the CLI's raw error.
	data := claudecli.PluginData{
		Installed: []claudecli.InstalledPlugin{
			{ID: fooID, Version: "1.0.0", Enabled: true, Scope: "project"},
		},
	}
	runner := &claudecli.FakeRunner{}
	m := modelWithCells(t, runner, data)

	m, cmd := press(t, m, "u")
	if cmd != nil || len(runner.Calls) != 0 {
		t.Fatal("action on a project-scope plugin reached the CLI")
	}
	if view := m.View(); !strings.Contains(view, "installed at project scope") {
		t.Errorf("scope hint missing:\n%s", view)
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
	if len(calls) != 1 || !slices.Equal(calls[0].Args, []string{"plugin", "disable", "--scope", "user", "foo@mp"}) {
		t.Errorf("disable did not target selected row foo@mp: %v", runner.Calls)
	}
}
