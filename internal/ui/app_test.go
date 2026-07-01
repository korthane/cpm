package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

var testProfiles = []config.Profile{
	{Path: "/home/u/.claude", Label: "personal"},
	{Path: "/home/u/.claude-work", Label: "work"},
}

func okRunner() *claudecli.FakeRunner {
	return &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			"plugin list --available --json": {
				Stdout: []byte(`{"installed":[{"id":"foo@mp","version":"1.0.0","enabled":true}],"available":[]}`),
			},
			"auth status --json": {
				Stdout: []byte(`{"loggedIn":true,"email":"u@example.com","subscriptionType":"pro"}`),
			},
		},
	}
}

// drain invokes cmd (flattening batches) and returns all produced messages.
func drain(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range batch {
			msgs = append(msgs, drain(t, c)...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

func TestInitFiresLoadPerProfile(t *testing.T) {
	m := New(okRunner(), testProfiles)

	var loaded, ticks int
	seen := map[int]bool{}
	for _, msg := range drain(t, m.Init()) {
		switch msg := msg.(type) {
		case profileLoadedMsg:
			loaded++
			seen[msg.index] = true
		case spinner.TickMsg:
			ticks++
		default:
			t.Errorf("unexpected message %T", msg)
		}
	}
	if loaded != len(testProfiles) {
		t.Errorf("got %d profileLoadedMsg, want %d", loaded, len(testProfiles))
	}
	if ticks != len(testProfiles) {
		t.Errorf("got %d spinner ticks, want %d", ticks, len(testProfiles))
	}
	for i := range testProfiles {
		if !seen[i] {
			t.Errorf("no profileLoadedMsg for profile %d", i)
		}
	}
}

func TestInitLoadCarriesProfileData(t *testing.T) {
	m := New(okRunner(), testProfiles[:1])

	for _, msg := range drain(t, m.Init()) {
		loaded, ok := msg.(profileLoadedMsg)
		if !ok {
			continue
		}
		if loaded.auth.Email != "u@example.com" {
			t.Errorf("auth email = %q, want u@example.com", loaded.auth.Email)
		}
		if len(loaded.plugins.Installed) != 1 {
			t.Errorf("got %d installed plugins, want 1", len(loaded.plugins.Installed))
		}
		return
	}
	t.Fatal("no profileLoadedMsg produced")
}

func TestLoadErrorProducesErrMsg(t *testing.T) {
	runner := &claudecli.FakeRunner{
		Default: claudecli.FakeResponse{Err: errors.New("boom")},
	}
	m := New(runner, testProfiles[:1])

	for _, msg := range drain(t, m.Init()) {
		if errMsg, ok := msg.(profileErrMsg); ok {
			if errMsg.index != 0 {
				t.Errorf("index = %d, want 0", errMsg.index)
			}
			if errMsg.err == nil {
				t.Error("err is nil, want boom")
			}
			return
		}
	}
	t.Fatal("no profileErrMsg produced")
}

func TestAuthFailureDegradesToBlankHeader(t *testing.T) {
	runner := okRunner()
	runner.Responses["auth status --json"] = claudecli.FakeResponse{Err: errors.New("logged out")}
	m := New(runner, testProfiles[:1])

	for _, msg := range drain(t, m.Init()) {
		if loaded, ok := msg.(profileLoadedMsg); ok {
			if loaded.auth.Email != "" {
				t.Errorf("auth email = %q, want empty on auth failure", loaded.auth.Email)
			}
			return
		}
	}
	t.Fatal("auth failure should not fail the column load; no profileLoadedMsg produced")
}

func TestProfileLoadedFlipsOnlyThatColumn(t *testing.T) {
	m := New(okRunner(), testProfiles)

	updated, _ := m.Update(profileLoadedMsg{
		index:   1,
		auth:    claudecli.AuthStatus{Email: "u@example.com"},
		plugins: claudecli.PluginData{},
	})
	got := updated.(Model)

	if got.columns[0].status != statusLoading {
		t.Errorf("column 0 status = %v, want loading", got.columns[0].status)
	}
	if got.columns[1].status != statusLoaded {
		t.Errorf("column 1 status = %v, want loaded", got.columns[1].status)
	}
	if got.columns[1].auth.Email != "u@example.com" {
		t.Errorf("column 1 email = %q, want u@example.com", got.columns[1].auth.Email)
	}
}

func TestProfileErrSetsErrorState(t *testing.T) {
	m := New(okRunner(), testProfiles)

	updated, _ := m.Update(profileErrMsg{index: 0, err: errors.New("boom")})
	got := updated.(Model)

	if got.columns[0].status != statusError {
		t.Errorf("column 0 status = %v, want error", got.columns[0].status)
	}
	if got.columns[0].err == nil {
		t.Error("column 0 err is nil")
	}
	if got.columns[1].status != statusLoading {
		t.Errorf("column 1 status = %v, want loading", got.columns[1].status)
	}
}

func TestSpinnerTickAdvancesOnlyLoadingColumns(t *testing.T) {
	m := New(okRunner(), testProfiles)
	loaded, _ := m.Update(profileLoadedMsg{index: 0})
	m = loaded.(Model)

	// A tick for the still-loading column keeps ticking.
	_, cmd := m.Update(spinner.TickMsg{ID: m.columns[1].spinner.ID()})
	if cmd == nil {
		t.Error("tick for loading column returned no follow-up command")
	}

	// A tick for the loaded column dies out.
	_, cmd = m.Update(spinner.TickMsg{ID: m.columns[0].spinner.ID()})
	if cmd != nil {
		t.Error("tick for loaded column returned a follow-up command, want none")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
	} {
		m := New(okRunner(), testProfiles)
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatalf("Update(%v) returned no command, want tea.Quit", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("Update(%v) did not return tea.Quit", key)
		}
	}
}

func TestTabSwitchChangesActiveView(t *testing.T) {
	m := New(okRunner(), testProfiles)
	if m.tab != tabPlugins {
		t.Fatalf("initial tab = %v, want plugins", m.tab)
	}
	before := m.View()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.tab != tabMCP {
		t.Errorf("tab after switch = %v, want MCP", got.tab)
	}
	if got.View() == before {
		t.Error("View unchanged after tab switch")
	}

	back, _ := got.Update(tea.KeyMsg{Type: tea.KeyTab})
	if back.(Model).tab != tabPlugins {
		t.Error("second tab press did not cycle back to plugins")
	}
}

func TestReloadResetsColumnsAndRefiresLoads(t *testing.T) {
	m := New(okRunner(), testProfiles)
	for i := range testProfiles {
		loaded, _ := m.Update(profileLoadedMsg{index: i})
		m = loaded.(Model)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := updated.(Model)

	for i := range got.columns {
		if got.columns[i].status != statusLoading {
			t.Errorf("column %d status after reload = %v, want loading", i, got.columns[i].status)
		}
	}

	var loads int
	for _, msg := range drain(t, cmd) {
		if _, ok := msg.(profileLoadedMsg); ok {
			loads++
		}
	}
	if loads != len(testProfiles) {
		t.Errorf("reload fired %d loads, want %d", loads, len(testProfiles))
	}
}

func TestViewShowsProfileLabelsAndStates(t *testing.T) {
	m := New(okRunner(), testProfiles)
	loaded, _ := m.Update(profileLoadedMsg{
		index: 0,
		auth:  claudecli.AuthStatus{Email: "u@example.com", SubscriptionType: "pro"},
	})
	m = loaded.(Model)
	errored, _ := m.Update(profileErrMsg{index: 1, err: errors.New("boom")})
	m = errored.(Model)

	view := m.View()
	for _, want := range []string{"personal", "work", "u@example.com", "boom"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q:\n%s", want, view)
		}
	}
}
