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

// mcpModelWithServers builds a model on the MCP tab with one loaded profile
// per server slice; profiles are named p0, p1, … with paths /h/p0, /h/p1, …
func mcpModelWithServers(t *testing.T, runner *claudecli.FakeRunner, perProfile ...[]claudecli.MCPServer) Model {
	t.Helper()
	profiles := make([]config.Profile, len(perProfile))
	for i := range perProfile {
		profiles[i] = config.Profile{Path: fmt.Sprintf("/h/p%d", i), Label: fmt.Sprintf("p%d", i)}
	}
	m := New(runner, profiles)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = resized.(Model)
	for i := range perProfile {
		loaded, _ := m.Update(profileLoadedMsg{index: i})
		m = loaded.(Model)
	}
	m, _ = switchToMCP(t, m)
	for i, servers := range perProfile {
		loaded, _ := m.Update(mcpLoadedMsg{index: i, servers: servers})
		m = loaded.(Model)
	}
	return m
}

// mcpRemoveCalls filters the fake's recorded calls down to `mcp remove <name>`
// invocations, excluding the mcp list reads issued by refreshes.
func mcpRemoveCalls(runner *claudecli.FakeRunner) []claudecli.FakeCall {
	var calls []claudecli.FakeCall
	for _, c := range runner.Calls {
		if len(c.Args) == 3 && c.Args[0] == "mcp" && c.Args[1] == "remove" {
			calls = append(calls, c)
		}
	}
	return calls
}

func TestMCPRemoveBlockedUntilConfirmed(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}})

	m, cmd := press(t, m, "x")
	if cmd != nil {
		t.Fatal("remove ran before confirmation")
	}
	if len(mcpRemoveCalls(runner)) != 0 {
		t.Fatal("remove CLI call recorded before confirmation")
	}
	if view := m.View(); !strings.Contains(view, "remove exa from p0? y/n") {
		t.Errorf("confirmation prompt missing:\n%s", view)
	}

	// Any key but y cancels.
	m, cmd = press(t, m, "n")
	if cmd != nil || len(mcpRemoveCalls(runner)) != 0 {
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
	calls := mcpRemoveCalls(runner)
	if len(calls) != 1 {
		t.Fatalf("got %d remove calls after confirm, want 1 (all calls: %v)", len(calls), runner.Calls)
	}
	if calls[0].ProfileDir != "/h/p0" {
		t.Errorf("profile dir = %q, want /h/p0", calls[0].ProfileDir)
	}
	if !slices.Equal(calls[0].Args, []string{"mcp", "remove", "exa"}) {
		t.Errorf("args = %v, want [mcp remove exa]", calls[0].Args)
	}
}

func TestMCPRemoveTargetsSelectedCell(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	servers := []claudecli.MCPServer{
		{Name: "atlassian", Target: "https://mcp.atlassian.com/v1/mcp"},
		{Name: "exa", Target: "https://mcp.exa.ai/mcp"},
	}
	m := mcpModelWithServers(t, runner, servers, servers)
	m, _ = press(t, m, "right")
	m, _ = press(t, m, "down")

	m, _ = press(t, m, "x")
	_, cmd := press(t, m, "y")
	if cmd == nil {
		t.Fatal("confirmed remove produced no command")
	}
	cmd()
	calls := mcpRemoveCalls(runner)
	if len(calls) != 1 {
		t.Fatalf("got %d remove calls, want 1", len(calls))
	}
	if calls[0].ProfileDir != "/h/p1" {
		t.Errorf("profile dir = %q, want /h/p1", calls[0].ProfileDir)
	}
	// Rows sort by name: row 1 is exa.
	if !slices.Equal(calls[0].Args, []string{"mcp", "remove", "exa"}) {
		t.Errorf("args = %v, want [mcp remove exa]", calls[0].Args)
	}
}

func TestMCPRemoveSuccessRefreshesProfile(t *testing.T) {
	runner := &claudecli.FakeRunner{Responses: map[string]claudecli.FakeResponse{}}
	servers := []claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}}
	m := mcpModelWithServers(t, runner, servers, servers)

	m, _ = press(t, m, "x")
	m, cmd := press(t, m, "y")
	// The CLI now reports no servers; the post-remove refresh must pick it up.
	runner.Responses["mcp list"] = claudecli.FakeResponse{
		Stdout: []byte("No MCP servers configured.\n"),
	}
	updated, refresh := m.Update(cmd())
	got := updated.(Model)

	if got.columns[0].mcpStatus != statusLoading {
		t.Errorf("acted-on column mcp status = %v, want loading during refresh", got.columns[0].mcpStatus)
	}
	if got.columns[1].mcpStatus != statusLoaded {
		t.Errorf("other column mcp status = %v, want loaded (untouched)", got.columns[1].mcpStatus)
	}
	if view := got.View(); !strings.Contains(view, "remove exa in p0: done") {
		t.Errorf("success status missing:\n%s", view)
	}

	var loaded *mcpLoadedMsg
	for _, msg := range drain(t, refresh) {
		if l, ok := msg.(mcpLoadedMsg); ok {
			loaded = &l
		}
	}
	if loaded == nil {
		t.Fatal("refresh produced no mcpLoadedMsg")
	}
	if loaded.index != 0 {
		t.Fatalf("refresh loaded profile %d, want 0", loaded.index)
	}

	final, _ := got.Update(*loaded)
	view := final.(Model).View()
	// exa stays as a row (still in p1): its line must show p0's cell as
	// absent next to p1's still-present target.
	var exaLine string
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, "exa") {
			exaLine = line
			break
		}
	}
	if exaLine == "" {
		t.Fatalf("exa row missing after refresh:\n%s", view)
	}
	if !strings.Contains(exaLine, "—") || !strings.Contains(exaLine, "mcp.exa.ai") {
		t.Errorf("exa row = %q, want an absent p0 cell next to p1's target", exaLine)
	}
}

func TestMCPRemoveRefusesPluginProvidedServer(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "plugin:playwright:playwright", Target: "npx @playwright/mcp@latest"}})

	m, cmd := press(t, m, "x")
	if cmd != nil || m.pending != nil || len(mcpRemoveCalls(runner)) != 0 {
		t.Fatal("remove of a plugin-provided server was not blocked")
	}
	if view := m.View(); !strings.Contains(view, "provided by a plugin") {
		t.Errorf("plugin-provided hint missing:\n%s", view)
	}
}

func TestMCPRemoveRefusesFlagLikeName(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "--evil", Target: "cmd"}})

	m, cmd := press(t, m, "x")
	if cmd != nil || m.pending != nil || len(mcpRemoveCalls(runner)) != 0 {
		t.Fatal("flag-like server name was not blocked")
	}
	if view := m.View(); !strings.Contains(view, "looks like a CLI flag") {
		t.Errorf("refusal hint missing:\n%s", view)
	}
}

func TestMCPRemoveOnEmptyMatrixDoesNothing(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner, nil)

	if _, cmd := press(t, m, "x"); cmd != nil {
		t.Error("remove on an empty MCP matrix produced a command")
	}
	if len(mcpRemoveCalls(runner)) != 0 {
		t.Fatalf("empty-matrix remove reached the CLI: %v", runner.Calls)
	}
}

func TestMCPRemoveFailureSurfacesErrorAndKeepsState(t *testing.T) {
	runner := &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			"mcp remove exa": {Err: errors.New("boom")},
		},
	}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}})

	m, _ = press(t, m, "x")
	m, cmd := press(t, m, "y")
	updated, refresh := m.Update(cmd())
	got := updated.(Model)

	if refresh != nil {
		t.Error("failed remove triggered a refresh")
	}
	if got.columns[0].mcpStatus != statusLoaded {
		t.Errorf("column mcp status = %v, want loaded (unchanged)", got.columns[0].mcpStatus)
	}
	view := got.View()
	if !strings.Contains(view, "boom") {
		t.Errorf("error missing from status line:\n%s", view)
	}
	if !strings.Contains(view, "exa") {
		t.Errorf("MCP data lost after failed remove:\n%s", view)
	}
}

func TestMCPRemoveOnAbsentCellShowsHint(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}},
		nil)
	m, _ = press(t, m, "right")

	m, cmd := press(t, m, "x")
	if cmd != nil || len(mcpRemoveCalls(runner)) != 0 {
		t.Fatal("remove on absent cell ran")
	}
	if m.pending != nil {
		t.Fatal("remove on absent cell armed the confirmation prompt")
	}
	if view := m.View(); !strings.Contains(view, "cannot remove exa in p1") {
		t.Errorf("hint missing:\n%s", view)
	}
}

func TestMCPRemoveOnLoadingColumnShowsHint(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}})
	// Add a second profile whose MCP data has not arrived and select it.
	m.columns = append(m.columns, column{profile: config.Profile{Path: "/h/p1", Label: "p1"}})
	m, _ = press(t, m, "right")

	m, cmd := press(t, m, "x")
	if cmd != nil || len(mcpRemoveCalls(runner)) != 0 {
		t.Fatal("remove against a loading column ran")
	}
	if view := m.View(); !strings.Contains(view, "p1 is not loaded yet") {
		t.Errorf("hint missing:\n%s", view)
	}
}

func TestMCPInstallKeyShowsAddNotSupportedHint(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}},
		nil)
	m, _ = press(t, m, "right")

	m, cmd := press(t, m, "i")
	if cmd != nil || len(runner.Calls) != 0 {
		t.Fatal("i on the MCP tab reached the CLI")
	}
	if view := m.View(); !strings.Contains(view, "MCP add is not yet supported") {
		t.Errorf("add-not-supported hint missing:\n%s", view)
	}
}

func TestMCPTabHelpShowsRemoveKey(t *testing.T) {
	runner := &claudecli.FakeRunner{}
	m := mcpModelWithServers(t, runner,
		[]claudecli.MCPServer{{Name: "exa", Target: "https://mcp.exa.ai/mcp"}})

	view := m.View()
	if !strings.Contains(view, "x: remove") {
		t.Errorf("MCP tab help missing remove key:\n%s", view)
	}
	if strings.Contains(view, "x: uninstall") {
		t.Errorf("MCP tab help shows plugin actions:\n%s", view)
	}
}
