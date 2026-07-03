package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	if ticks != 1 {
		t.Errorf("got %d spinner ticks, want 1 (one shared spinner)", ticks)
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

func TestLoadRefreshesMarketplacesBeforeCatalogRead(t *testing.T) {
	runner := okRunner()
	m := New(runner, testProfiles[:1])
	drain(t, m.Init())

	joined := make([]string, len(runner.Calls))
	for i, c := range runner.Calls {
		joined[i] = strings.Join(c.Args, " ")
	}
	if len(joined) == 0 || joined[0] != "plugin marketplace update" {
		t.Fatalf("calls = %v, want marketplace update first", joined)
	}
	if !slices.Contains(joined, "plugin list --available --json") {
		t.Fatalf("calls = %v, want a plugin list after the refresh", joined)
	}
}

func TestLoadCarriesFreshLatestVersions(t *testing.T) {
	runner := okRunner()
	runner.Responses["plugin list --available --json"] = claudecli.FakeResponse{
		Stdout: []byte(`{
			"installed": [{"id": "foo@mp", "version": "1.0.0", "enabled": true}],
			"available": [{"pluginId": "foo@mp", "version": "1.2.0", "source": "./foo"}]
		}`),
	}
	m := New(runner, testProfiles[:1])

	for _, msg := range drain(t, m.Init()) {
		loaded, ok := msg.(profileLoadedMsg)
		if !ok {
			continue
		}
		if loaded.latest.Stale {
			t.Error("latest.Stale = true, want false")
		}
		pid := claudecli.PluginID{Name: "foo", Marketplace: "mp"}
		if v := loaded.latest.Versions[pid]; v != "1.2.0" {
			t.Errorf("latest[foo@mp] = %q, want 1.2.0", v)
		}
		return
	}
	t.Fatal("no profileLoadedMsg produced")
}

func TestStaleRefreshFlaggedInPinnedHeader(t *testing.T) {
	fresh := New(okRunner(), testProfiles[:1])
	for _, msg := range drain(t, fresh.Init()) {
		updated, _ := fresh.Update(msg)
		fresh = updated.(Model)
	}
	if strings.Contains(fresh.View(), "(stale)") {
		t.Error("fresh load shows the stale marker")
	}

	runner := okRunner()
	runner.Responses["plugin marketplace update"] = claudecli.FakeResponse{
		Err: errors.New("marketplace source unreachable"),
	}
	stale := New(runner, testProfiles[:1])
	for _, msg := range drain(t, stale.Init()) {
		updated, _ := stale.Update(msg)
		stale = updated.(Model)
	}
	if !strings.Contains(stale.View(), "latest (stale)") {
		t.Errorf("refresh failure not flagged in pinned header:\n%s", stale.View())
	}
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
	runner.Responses["auth status --json"] = claudecli.FakeResponse{Err: errors.New("auth read failed")}
	m := New(runner, testProfiles[:1])

	loaded := false
	for _, msg := range drain(t, m.Init()) {
		if _, ok := msg.(profileLoadedMsg); ok {
			loaded = true
		}
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	if !loaded {
		t.Fatal("auth failure should not fail the column load; no profileLoadedMsg produced")
	}
	// A failed auth read is not the same as a logged-out account: the header
	// must stay blank rather than claim "not logged in".
	if view := m.View(); strings.Contains(view, "not logged in") {
		t.Errorf("auth failure rendered as logged-out header:\n%s", view)
	}
}

const (
	authLoggedOut = `{"loggedIn":false}`
	authLoggedIn  = `{"loggedIn":true,"email":"me@example.com","subscriptionType":"max"}`
)

// authCalls returns the profile dir of every `auth status --json` call, in
// order.
func authCalls(r *claudecli.FakeRunner) []string {
	var dirs []string
	for _, c := range r.Calls {
		if strings.Join(c.Args, " ") == "auth status --json" {
			dirs = append(dirs, c.ProfileDir)
		}
	}
	return dirs
}

// initModel drives Init and applies every produced message.
func initModel(t *testing.T, m Model) Model {
	t.Helper()
	for _, msg := range drain(t, m.Init()) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	return m
}

func TestDefaultProfileRechecksAuthWithEnvStripped(t *testing.T) {
	runner := okRunner()
	runner.ResponsesByDir = map[string]map[string]claudecli.FakeResponse{
		"/home/u/.claude": {"auth status --json": {Stdout: []byte(authLoggedOut)}},
		"":                {"auth status --json": {Stdout: []byte(authLoggedIn)}},
	}
	m := initModel(t, New(runner, []config.Profile{
		{Path: "/home/u/.claude", Label: "default", IsDefault: true},
	}))

	if got := authCalls(runner); !slices.Equal(got, []string{"/home/u/.claude", ""}) {
		t.Errorf("auth calls = %v, want the dir'd check then the env-stripped fallback", got)
	}
	if view := m.View(); !strings.Contains(view, "me@example.com") {
		t.Errorf("logged-in fallback result not rendered:\n%s", view)
	}
}

func TestNonDefaultProfileLoggedOutNeverRechecksAuth(t *testing.T) {
	runner := okRunner()
	runner.Responses["auth status --json"] = claudecli.FakeResponse{Stdout: []byte(authLoggedOut)}
	m := initModel(t, New(runner, testProfiles[:1]))

	if got := authCalls(runner); !slices.Equal(got, []string{"/home/u/.claude"}) {
		t.Errorf("auth calls = %v, want a single dir'd check", got)
	}
	if view := m.View(); !strings.Contains(view, "not logged in") {
		t.Errorf("logged-out non-default profile should render as logged out:\n%s", view)
	}
}

func TestDefaultProfileAuthErrorSkipsFallback(t *testing.T) {
	runner := okRunner()
	runner.ResponsesByDir = map[string]map[string]claudecli.FakeResponse{
		"/home/u/.claude": {"auth status --json": {Err: errors.New("auth read failed")}},
		"":                {"auth status --json": {Stdout: []byte(authLoggedIn)}},
	}
	m := initModel(t, New(runner, []config.Profile{
		{Path: "/home/u/.claude", Label: "default", IsDefault: true},
	}))

	// An errored read is not a clean logged-out answer: the fallback must not
	// mask it, and the header keeps the blank degradation.
	if got := authCalls(runner); !slices.Equal(got, []string{"/home/u/.claude"}) {
		t.Errorf("auth calls = %v, want no fallback after an auth error", got)
	}
	if view := m.View(); strings.Contains(view, "not logged in") ||
		strings.Contains(view, "me@example.com") {
		t.Errorf("auth error should keep the blank header:\n%s", view)
	}
}

func TestDefaultProfileKeepsLoggedOutUnlessFallbackIsLoggedIn(t *testing.T) {
	cases := map[string]claudecli.FakeResponse{
		"fallback logged out": {Stdout: []byte(authLoggedOut)},
		"fallback errors":     {Err: errors.New("keychain unavailable")},
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			runner := okRunner()
			runner.ResponsesByDir = map[string]map[string]claudecli.FakeResponse{
				"/home/u/.claude": {"auth status --json": {Stdout: []byte(authLoggedOut)}},
				"":                {"auth status --json": resp},
			}
			m := initModel(t, New(runner, []config.Profile{
				{Path: "/home/u/.claude", Label: "default", IsDefault: true},
			}))

			if got := authCalls(runner); !slices.Equal(got, []string{"/home/u/.claude", ""}) {
				t.Errorf("auth calls = %v, want dir'd check then fallback", got)
			}
			// Only a clean logged-in fallback wins; anything else keeps the
			// original clean logged-out answer.
			if view := m.View(); !strings.Contains(view, "not logged in") {
				t.Errorf("profile should stay logged out:\n%s", view)
			}
		})
	}
}

func TestRefreshProfileAppliesDefaultAuthFallback(t *testing.T) {
	runner := okRunner()
	runner.ResponsesByDir = map[string]map[string]claudecli.FakeResponse{
		"/home/u/.claude": {"auth status --json": {Stdout: []byte(authLoggedOut)}},
		"":                {"auth status --json": {Stdout: []byte(authLoggedIn)}},
	}
	profile := config.Profile{Path: "/home/u/.claude", Label: "default", IsDefault: true}

	msg, ok := refreshProfile(runner, 0, 1, profile, false)().(profileLoadedMsg)
	if !ok {
		t.Fatal("refreshProfile did not produce a profileLoadedMsg")
	}
	if !msg.auth.LoggedIn || msg.auth.Email != "me@example.com" {
		t.Errorf("refresh auth = %+v, want the logged-in fallback result", msg.auth)
	}
}

func TestStatusLineTruncatedToTerminalWidth(t *testing.T) {
	// rowWindow budgets exactly one row for the status line; a longer one
	// would soft-wrap and push the header chrome off the alt screen.
	m := modelWithCells(t, &claudecli.FakeRunner{}, installedFoo(true))
	m.setStatus(strings.Repeat("boom ", 100), true)

	if got := lipgloss.Width(m.statusLine()); got > m.width {
		t.Errorf("status line is %d cells wide, want at most %d", got, m.width)
	}
}

func TestFailedReloadColumnProducesNoPhantomRows(t *testing.T) {
	barData := claudecli.PluginData{
		Installed: []claudecli.InstalledPlugin{
			{ID: claudecli.PluginID{Name: "bar", Marketplace: "mp"}, Version: "1.0.0", Enabled: true},
		},
	}
	m := modelWithCells(t, &claudecli.FakeRunner{}, installedFoo(true), barData)

	// The failed reload keeps the column's stale data, but its cells render
	// blank, so a row owned only by that column would show no owner at all.
	updated, _ := m.Update(profileErrMsg{index: 1, err: errors.New("reload failed")})
	m = updated.(Model)

	groups, _ := m.pluginGroups()
	if len(groups) != 1 || len(groups[0].Plugins) != 1 || groups[0].Plugins[0].ID.Name != "foo" {
		t.Fatalf("groups after failed reload = %+v, want only foo under mp", groups)
	}
}

func TestLoggedInWithoutAccountFieldsShowsFallbackHeader(t *testing.T) {
	runner := okRunner()
	runner.Responses["auth status --json"] = claudecli.FakeResponse{
		Stdout: []byte(`{"loggedIn": true}`),
	}
	m := New(runner, testProfiles[:1])

	for _, msg := range drain(t, m.Init()) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	if view := m.View(); !strings.Contains(view, "logged in") ||
		strings.Contains(view, "not logged in") {
		t.Errorf("logged-in profile without email/plan should show the "+
			"\"logged in\" fallback header:\n%s", view)
	}
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

func TestSpinnerTickAliveOnlyWhileColumnsLoading(t *testing.T) {
	m := New(okRunner(), testProfiles)
	loaded, _ := m.Update(profileLoadedMsg{index: 0})
	m = loaded.(Model)

	// One column still loading: the tick keeps rescheduling itself.
	updated, cmd := m.Update(spinner.TickMsg{ID: m.spinner.ID()})
	m = updated.(Model)
	if cmd == nil {
		t.Error("tick returned no follow-up command while a column is loading")
	}

	// Everything loaded: the tick dies out.
	loaded, _ = m.Update(profileLoadedMsg{index: 1})
	m = loaded.(Model)
	if _, cmd := m.Update(spinner.TickMsg{ID: m.spinner.ID()}); cmd != nil {
		t.Error("tick returned a follow-up command with nothing loading, want none")
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
		auth:  claudecli.AuthStatus{LoggedIn: true, Email: "u@example.com", SubscriptionType: "pro"},
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
