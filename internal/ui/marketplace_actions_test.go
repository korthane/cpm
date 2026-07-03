package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/korthane/cpm/internal/claudecli"
)

// marketplaceCalls filters the fake's recorded calls down to
// `plugin marketplace <verb> <arg…>` action invocations. The length guard
// excludes the argument-less `plugin marketplace update` a fresh load runs
// and the `plugin marketplace list --json` reads.
func marketplaceCalls(runner *claudecli.FakeRunner, verb string) []claudecli.FakeCall {
	var calls []claudecli.FakeCall
	for _, c := range runner.Calls {
		if len(c.Args) >= 4 && c.Args[0] == "plugin" && c.Args[1] == "marketplace" && c.Args[2] == verb {
			calls = append(calls, c)
		}
	}
	return calls
}

func TestMarketplaceActionKeysInvokeCorrectCLI(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		verb     string
		col      int
		wantArgs []string
	}{
		// p1 lacks the marketplace, and p0 knows its source: add is valid there.
		{"add where missing", "i", "add", 1,
			[]string{"plugin", "marketplace", "add", "owner/mp", "--scope", "user"}},
		// update has no scope flag (none exists in the CLI).
		{"update where configured", "u", "update", 0,
			[]string{"plugin", "marketplace", "update", "mp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
			m := modelWithCells(t, runner, p0, claudecli.PluginData{})
			// Row 0 is the mp marketplace header.
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

			calls := marketplaceCalls(runner, tt.verb)
			if len(calls) != 1 {
				t.Fatalf("got %d %q calls, want 1 (all calls: %v)", len(calls), tt.verb, runner.Calls)
			}
			wantDir := "/h/p" + string(rune('0'+tt.col))
			if calls[0].ProfileDir != wantDir {
				t.Errorf("profile dir = %q, want %q", calls[0].ProfileDir, wantDir)
			}
			if !slices.Equal(calls[0].Args, tt.wantArgs) {
				t.Errorf("args = %v, want %v", calls[0].Args, tt.wantArgs)
			}
		})
	}
}

func TestMarketplaceRemoveBlockedUntilConfirmed(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, runner, p0)

	m, cmd := press(t, m, "x")
	if cmd != nil {
		t.Fatal("marketplace remove ran before confirmation")
	}
	if len(marketplaceCalls(runner, "remove")) != 0 {
		t.Fatal("remove CLI call recorded before confirmation")
	}
	if view := m.View(); !strings.Contains(view, "remove marketplace mp from p0? y/n") {
		t.Errorf("confirmation prompt missing:\n%s", view)
	}

	// Any key but y cancels.
	m, cmd = press(t, m, "n")
	if cmd != nil || len(marketplaceCalls(runner, "remove")) != 0 {
		t.Fatal("cancelled remove still ran")
	}
	if view := m.View(); strings.Contains(view, "y/n") {
		t.Errorf("prompt still shown after cancel:\n%s", view)
	}

	m, _ = press(t, m, "x")
	_, cmd = press(t, m, "y")
	if cmd == nil {
		t.Fatal("confirmed remove produced no command")
	}
	cmd()
	calls := marketplaceCalls(runner, "remove")
	if len(calls) != 1 {
		t.Fatalf("got %d remove calls after confirm, want 1", len(calls))
	}
	// --scope user is mandatory: omitting it removes from ALL scopes.
	want := []string{"plugin", "marketplace", "remove", "mp", "--scope", "user"}
	if !slices.Equal(calls[0].Args, want) {
		t.Errorf("args = %v, want %v", calls[0].Args, want)
	}
}

func TestMarketplaceAddRefusals(t *testing.T) {
	// A github marketplace whose repo differs per profile (source conflict).
	conflicted := func(repo string) claudecli.PluginData {
		return claudecli.PluginData{Marketplaces: []claudecli.Marketplace{
			{Name: "mp", Source: "github", Repo: repo},
		}}
	}
	tests := []struct {
		name string
		data []claudecli.PluginData
		col  int
		hint string
	}{
		{"already configured",
			[]claudecli.PluginData{withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")},
			0, "already configured"},
		{"source conflict",
			[]claudecli.PluginData{conflicted("owner/mp"), conflicted("other/mp"), {}},
			2, "profiles disagree"},
		// An orphaned marketplace (referenced by an installed plugin, configured
		// nowhere) has no known source to add from.
		{"no known source",
			[]claudecli.PluginData{installedFoo(true), {}},
			1, "no known source"},
		{"flag-like source arg",
			[]claudecli.PluginData{{Marketplaces: []claudecli.Marketplace{
				{Name: "mp", Source: "github", Repo: "--evil"},
			}}, {}},
			1, "looks like a CLI flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			m := modelWithCells(t, runner, tt.data...)
			for range tt.col {
				m, _ = press(t, m, "right")
			}

			m, cmd := press(t, m, "i")
			if cmd != nil {
				t.Fatal("refused add still produced a command")
			}
			if len(marketplaceCalls(runner, "add")) != 0 {
				t.Fatalf("refused add reached the CLI: %v", runner.Calls)
			}
			if view := m.View(); !strings.Contains(view, tt.hint) {
				t.Errorf("hint %q missing:\n%s", tt.hint, view)
			}
		})
	}
}

func TestMarketplaceUpdateAndRemoveRefusedWhenNotConfigured(t *testing.T) {
	for _, key := range []string{"u", "x"} {
		t.Run(key, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
			m := modelWithCells(t, runner, p0, claudecli.PluginData{})
			m, _ = press(t, m, "right") // p1: marketplace not configured

			m, cmd := press(t, m, key)
			if cmd != nil || len(runner.Calls) != 0 {
				t.Fatalf("key %q on an unconfigured marketplace cell acted", key)
			}
			view := m.View()
			if !strings.Contains(view, "not configured") {
				t.Errorf("hint missing:\n%s", view)
			}
			if strings.Contains(view, "y/n") {
				t.Errorf("refused remove still armed the confirmation:\n%s", view)
			}
		})
	}
}

func TestMarketplaceActionsRefusedWhenStateUnknown(t *testing.T) {
	// The profile's marketplace list failed: Configured=false on its cells
	// reflects nothing, so add could duplicate an existing marketplace and
	// update/remove could target one that is actually there — refuse all.
	for _, key := range []string{"i", "u", "x"} {
		t.Run(key, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			data := installedFoo(true) // the mp group exists via the installed plugin
			data.MarketplacesUnknown = true
			m := modelWithCells(t, runner, data)

			m, cmd := press(t, m, key)
			if cmd != nil || len(runner.Calls) != 0 {
				t.Fatalf("key %q with unknown marketplace state acted", key)
			}
			view := m.View()
			if !strings.Contains(view, "marketplace state unknown") {
				t.Errorf("hint missing:\n%s", view)
			}
			if strings.Contains(view, "y/n") {
				t.Errorf("refused remove still armed the confirmation:\n%s", view)
			}
		})
	}
}

func TestMarketplaceActionRefreshReloadsStartedMCPTab(t *testing.T) {
	// A marketplace remove can uninstall plugins that provide
	// plugin:<plugin>:<name> MCP servers, so a loaded MCP tab must reload
	// the acted-on column, exactly like plugin actions.
	runner := &claudecli.FakeRunner{}
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, runner, p0)
	m, _ = press(t, m, "tab") // starts the lazy MCP load
	loaded, _ := m.Update(mcpLoadedMsg{index: 0, gen: m.columns[0].mcpGen,
		servers: []claudecli.MCPServer{{Name: "plugin:foo:srv", Target: "stdio"}}})
	m = loaded.(Model)
	m, _ = press(t, m, "tab") // back to plugins; row 0 is the mp header

	m, _ = press(t, m, "x") // arm the remove confirmation
	m, cmd := press(t, m, "y")
	if cmd == nil {
		t.Fatal("confirmed remove produced no command")
	}
	updated, refresh := m.Update(cmd())
	got := updated.(Model)

	if got.columns[0].mcpStatus != statusLoading {
		t.Errorf("mcpStatus = %v, want loading after marketplace remove", got.columns[0].mcpStatus)
	}
	var mcpReloaded bool
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(mcpLoadedMsg); ok && l.gen == got.columns[0].mcpGen {
			mcpReloaded = true
		}
	}
	if !mcpReloaded {
		t.Error("post-remove refresh fired no MCP reload for the acted column")
	}
}

func TestSuccessfulSingleMarketplaceUpdateClearsStale(t *testing.T) {
	// The stale marker comes from a failed bulk refresh during load; a
	// successful per-marketplace update of the profile's ONLY marketplace
	// freshened everything the marker covers, so the refresh must drop it.
	runner := &claudecli.FakeRunner{Responses: map[string]claudecli.FakeResponse{
		"plugin list --available --json": {Stdout: []byte(`{"installed":[],"available":[]}`)},
		"plugin marketplace list --json": {Stdout: []byte(`[{"name":"mp","source":"github","repo":"owner/mp"}]`)},
	}}
	p0 := withMarketplace(claudecli.PluginData{}, "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, runner, p0)
	m.columns[0].latest.Stale = true

	updated, refresh := m.Update(actionDoneMsg{index: 0, verb: "update marketplace", target: "mp"})
	m = updated.(Model)
	var stale, found bool
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(profileLoadedMsg); ok {
			found, stale = true, l.latest.Stale
		}
	}
	if !found {
		t.Fatal("post-update refresh produced no profile load")
	}
	if stale {
		t.Error("Stale still set after a successful update of the only marketplace")
	}
}

func TestSuccessfulUpdateKeepsStaleWithOtherMarketplaces(t *testing.T) {
	// Only one of the profile's two marketplaces was updated; the other's
	// catalog is still whatever the failed bulk refresh left behind, so the
	// conservative stale marker must survive.
	runner := &claudecli.FakeRunner{Responses: map[string]claudecli.FakeResponse{
		"plugin list --available --json": {Stdout: []byte(`{"installed":[],"available":[]}`)},
		"plugin marketplace list --json": {Stdout: []byte(`[{"name":"mp","source":"github","repo":"owner/mp"}]`)},
	}}
	p0 := withMarketplace(withMarketplace(claudecli.PluginData{}, "mp", "a1b2c3", "2026-06-28"),
		"mp2", "d4e5f6", "2026-07-01")
	m := modelWithCells(t, runner, p0)
	m.columns[0].latest.Stale = true

	updated, refresh := m.Update(actionDoneMsg{index: 0, verb: "update marketplace", target: "mp"})
	m = updated.(Model)
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(profileLoadedMsg); ok && !l.latest.Stale {
			t.Error("Stale dropped although a second marketplace stayed unrefreshed")
		}
	}
}

func TestMarketplaceActionRefusesFlagLikeName(t *testing.T) {
	for _, key := range []string{"u", "x"} {
		t.Run(key, func(t *testing.T) {
			runner := &claudecli.FakeRunner{}
			data := claudecli.PluginData{Marketplaces: []claudecli.Marketplace{
				{Name: "-mp", Source: "github", Repo: "owner/mp"},
			}}
			m := modelWithCells(t, runner, data)

			m, cmd := press(t, m, key)
			if cmd != nil || len(runner.Calls) != 0 {
				t.Fatal("flag-like marketplace name reached the CLI")
			}
			if view := m.View(); !strings.Contains(view, "looks like a CLI flag") {
				t.Errorf("refusal hint missing:\n%s", view)
			}
		})
	}
}

func TestPluginOnlyKeysNoopOnMarketplaceRow(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, runner, p0)

	for _, key := range []string{"e", "d"} {
		if _, cmd := press(t, m, key); cmd != nil {
			t.Errorf("key %q on a marketplace row produced a command", key)
		}
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("plugin-only key on a marketplace row reached the CLI: %v", runner.Calls)
	}
}

func TestMarketplaceActionBusyGated(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, runner, p0)

	m, cmd := press(t, m, "u")
	if cmd == nil {
		t.Fatal("first action produced no command")
	}
	// The first action has not completed: a second mutating action against the
	// same profile must be rejected — including plugin actions on other rows.
	m, cmd2 := press(t, m, "u")
	if cmd2 != nil {
		t.Fatal("second marketplace action ran while the first was in flight")
	}
	if view := m.View(); !strings.Contains(view, "action in progress") {
		t.Errorf("busy hint missing:\n%s", view)
	}

	// Completing the first action clears the guard and refreshes the column.
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if m.columns[0].busy {
		t.Error("column still busy after the action completed")
	}
	if refresh == nil {
		t.Error("completed marketplace action triggered no refresh")
	}
	if m.columns[0].status != statusLoading {
		t.Errorf("column status = %v, want loading during refresh", m.columns[0].status)
	}
}

func TestMarketplaceActionOnLoadingColumnShowsHint(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	p0 := withMarketplace(installedFoo(true), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, runner, p0)
	// Add a second, still-loading profile and select it.
	m.columns = append(m.columns, column{profile: m.columns[0].profile})
	m.columns[1].profile.Label = "p1"
	m, _ = press(t, m, "right")

	m, cmd := press(t, m, "u")
	if cmd != nil || len(runner.Calls) != 0 {
		t.Fatal("marketplace action against a loading column ran")
	}
	if view := m.View(); !strings.Contains(view, "p1 is not loaded yet") {
		t.Errorf("hint missing:\n%s", view)
	}
}

func TestMarketplaceActionTimeoutIsUncertain(t *testing.T) {
	// Same contract as plugin actions: uncertain must come from the command's
	// own expired context, not merely a failed CLI call.
	old := cmdTimeout
	cmdTimeout = 10 * time.Millisecond
	t.Cleanup(func() { cmdTimeout = old })

	msg, ok := runMarketplaceAction(ctxWaitRunner{}, 0, "/h/p0", "update", "mp", "")().(actionDoneMsg)
	if !ok || msg.err == nil || !msg.uncertain {
		t.Errorf("timed-out marketplace action = %+v, want err and uncertain", msg)
	}
}

func TestMarketplaceActionPlainFailureIsNotUncertain(t *testing.T) {
	runner := &claudecli.FakeRunner{Default: claudecli.FakeResponse{Err: errors.New("boom")}}
	msg := runMarketplaceAction(runner, 0, "/h/p0", "update", "mp", "")().(actionDoneMsg)
	if msg.err == nil || msg.uncertain {
		t.Errorf("failed marketplace action = %+v, want err and not uncertain", msg)
	}
}
