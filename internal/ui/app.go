// Package ui holds the Bubble Tea models and views: the root tabbed app and
// the per-profile comparison tables.
package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/model"
)

// cmdTimeout bounds each UI-fired command (one shared context per tea.Cmd, so
// a load's whole CLI sequence draws from one budget): marketplace update hits
// the network and mcp list health-checks every server, so a hung CLI must
// degrade to the column's error state instead of spinning forever. A var only
// so tests can shorten it to drive a real deadline expiry.
var cmdTimeout = 2 * time.Minute

type tab int

const (
	tabPlugins tab = iota
	tabMCP
	tabCount
)

var tabNames = [tabCount]string{"Plugins", "MCP"}

func (t tab) String() string { return tabNames[t] }

type loadStatus int

const (
	statusLoading loadStatus = iota
	statusLoaded
	statusError
)

// column is one profile's slice of app state: its identity plus the data and
// load status filled in asynchronously by that profile's load command.
type column struct {
	profile config.Profile
	status  loadStatus
	// gen / mcpGen stamp each async load; a result whose stamp is no longer
	// current is dropped. mcpGen is load-bearing: a post-action MCP reload
	// fires while an `mcp list` that read mid-mutation may still be in flight,
	// and the stamp drops that stale result. Plugin loads are all gated on the
	// column being idle (Init runs once, reloads skip busy/loading columns,
	// actions hold busy until their refresh), so gen guards no reachable race
	// today — it is insurance against a regression in that gating.
	gen    int
	mcpGen int
	// busy marks a mutating CLI action in flight against this profile; action
	// keys are rejected until it completes so two writes cannot race on one
	// config dir.
	busy bool
	auth claudecli.AuthStatus
	// authErr marks a failed auth read; the header degrades to blank instead
	// of claiming "not logged in", which auth alone can't distinguish from a
	// transient failure.
	authErr error
	plugins claudecli.PluginData
	// latest carries the profile's freshly resolved latest versions (its
	// marketplaces are re-fetched on every load; Stale marks a failed fetch).
	latest claudecli.LatestVersions
	err    error
	// MCP state loads lazily (mcp list is slow) and independently of the
	// plugin data, so it carries its own status/error pair.
	mcp       []claudecli.MCPServer
	mcpStatus loadStatus
	mcpErr    error
}

// Model is the root Bubble Tea model: tab state plus one column per profile.
type Model struct {
	runner  claudecli.Runner
	tab     tab
	columns []column
	// selRow / selCol address the selected matrix cell; the view scrolls
	// horizontally and vertically so the selected cell stays visible.
	selRow int
	selCol int
	width  int
	height int
	// spinner is shared by every loading cell; its tick stays alive while
	// anything is still loading.
	spinner spinner.Model
	// mcpStarted flips on the first view of the MCP tab: mcp list runs a
	// health check per server, so it only loads once actually needed.
	mcpStarted bool
	// folded is the per-marketplace fold state of the plugins tab (name →
	// folded); keyed by name so it survives reloads. Not persisted across
	// runs. Allocated lazily on the first toggle.
	folded map[string]bool
	// filters holds the applied fuzzy name query per tab ("" == no filter);
	// each tab keeps its own so switching tabs does not carry a query over.
	filters [tabCount]string
	// filterInput edits the active tab's query; while filterEditing is true it
	// takes every key, so the navigation, action and quit keys type literal
	// runes instead of firing.
	filterInput   textinput.Model
	filterEditing bool
	// pending is a destructive action awaiting y/n confirmation.
	pending *pendingAction
	// status is the transient status/error line; cleared on the next key.
	status    string
	statusErr bool
}

// pendingAction is an action held back behind the confirmation prompt.
type pendingAction struct {
	verb string
	// target is the plugin id, MCP server name, or marketplace name the action
	// applies to; mcp/marketplace say which, and thereby which CLI command a
	// confirmation fires.
	target      string
	mcp         bool
	marketplace bool
	col         int
}

// New builds the root model for the given profiles. All columns start in the
// loading state; Init fires the loads.
func New(r claudecli.Runner, profiles []config.Profile) Model {
	columns := make([]column, len(profiles))
	for i, p := range profiles {
		columns[i] = column{profile: p}
	}
	input := textinput.New()
	input.Prompt = filterPrompt
	// A blinking cursor would keep the event loop (and the tests) ticking for
	// a caret nobody needs in a one-line filter box.
	input.Cursor.SetMode(cursor.CursorStatic)
	return Model{
		runner:      r,
		columns:     columns,
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		filterInput: input,
	}
}

// filterPrompt labels the filter input and the closed-but-active indicator.
const filterPrompt = "filter: "

// profileLoadedMsg delivers one profile's data; index addresses the column
// and gen the load generation it belongs to.
type profileLoadedMsg struct {
	index   int
	gen     int
	auth    claudecli.AuthStatus
	authErr error
	plugins claudecli.PluginData
	latest  claudecli.LatestVersions
}

// profileErrMsg reports a failed profile load.
type profileErrMsg struct {
	index int
	gen   int
	err   error
}

// mcpLoadedMsg delivers one profile's MCP servers; index addresses the column
// and gen the load generation it belongs to.
type mcpLoadedMsg struct {
	index   int
	gen     int
	servers []claudecli.MCPServer
}

// mcpErrMsg reports a failed MCP load for one profile.
type mcpErrMsg struct {
	index int
	gen   int
	err   error
}

// actionDoneMsg reports a finished plugin or marketplace action against one
// profile; target is the plugin id or marketplace name. uncertain marks a
// timed-out action: the CLI was killed mid-flight, so the write may have
// (partially) applied and the column data cannot be trusted.
type actionDoneMsg struct {
	index     int
	verb      string
	target    string
	err       error
	uncertain bool
	// mutated marks a failed action that still changed the profile's config —
	// the implicit marketplace add applied before the chained install failed —
	// so the column must reload despite the clean failure.
	mutated bool
}

// marketplaceAddedMsg reports the implicit `plugin marketplace add` that
// precedes installing a plugin into a profile lacking its marketplace; on
// success the handler chains the install command, so the status line can
// update between the two steps.
type marketplaceAddedMsg struct {
	index     int
	name      string
	plugin    claudecli.PluginID
	err       error
	uncertain bool
}

// mcpActionDoneMsg reports a finished MCP remove against one profile; see
// actionDoneMsg for uncertain.
type mcpActionDoneMsg struct {
	index     int
	server    string
	err       error
	uncertain bool
}

// Init fans out one load command per profile (they run in parallel) plus the
// spinner tick. Columns start in the loading state, so this cannot share the
// statusLoading gate reloadPlugins uses.
func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.columns)+1)
	for i := range m.columns {
		m.columns[i].gen++
		cmds = append(cmds, loadProfile(m.runner, i, m.columns[i].gen, m.columns[i].profile))
	}
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

// reloadPlugins refires the plugin load for every idle column. Busy columns
// (mutating action in flight) and still-loading columns are skipped: the
// fresh load runs `plugin marketplace update` (a write), and two writers on
// one config dir can corrupt it — the gen stamp only drops a superseded
// load's result, it cannot stop its in-flight process. Skipped columns keep
// their state; the post-action refresh / in-flight load covers them.
func (m Model) reloadPlugins() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.columns)+1)
	for i := range m.columns {
		col := &m.columns[i]
		if col.busy || col.status == statusLoading {
			continue
		}
		col.status = statusLoading
		col.err = nil
		col.gen++
		cmds = append(cmds, loadProfile(m.runner, i, col.gen, col.profile))
	}
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

// reloadMCP refires the MCP load for every column without one already in
// flight. Unlike reloadPlugins it deliberately does not skip busy columns —
// `mcp list` is read-only, so it cannot become a second writer — but it does
// skip loading ones: a stacked reload could not corrupt anything, yet each
// extra run health-checks every server, piling up expensive processes whose
// results the gen stamp then throws away.
func (m Model) reloadMCP() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.columns)+1)
	for i := range m.columns {
		col := &m.columns[i]
		if col.mcpStatus == statusLoading {
			continue
		}
		col.mcpStatus = statusLoading
		col.mcpErr = nil
		col.mcpGen++
		cmds = append(cmds, loadMCPProfile(m.runner, i, col.mcpGen, col.profile.Path))
	}
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

// loadMCPAll fans out one MCP load per profile plus the spinner tick; used on
// the first view of the MCP tab, where every column's zero-value mcpStatus is
// statusLoading with nothing actually in flight — reloadMCP's gate would skip
// them all.
func (m Model) loadMCPAll() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.columns)+1)
	for i := range m.columns {
		m.columns[i].mcpGen++
		cmds = append(cmds, loadMCPProfile(m.runner, i, m.columns[i].mcpGen, m.columns[i].profile.Path))
	}
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

func loadMCPProfile(r claudecli.Runner, index, gen int, profileDir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		servers, err := claudecli.LoadMCP(ctx, r, profileDir)
		if err != nil {
			return mcpErrMsg{index: index, gen: gen, err: err}
		}
		return mcpLoadedMsg{index: index, gen: gen, servers: servers}
	}
}

func loadProfile(r claudecli.Runner, index, gen int, profile config.Profile) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		// The fresh load re-fetches the profile's marketplaces so the pinned
		// latest versions never come from a stale cache (user requirement).
		plugins, latest, err := claudecli.LoadPluginsFresh(ctx, r, profile.Path)
		if err != nil {
			return profileErrMsg{index: index, gen: gen, err: err}
		}
		// A failed auth read degrades to a blank header instead of failing
		// the whole column. (A logged-out profile is not a failure: the CLI
		// still prints parseable JSON with loggedIn=false.)
		auth, authErr := loadAuth(ctx, r, profile)
		return profileLoadedMsg{index: index, gen: gen, auth: auth, authErr: authErr,
			plugins: plugins, latest: latest}
	}
}

// loadAuth reads a profile's auth status with the default-profile fallback:
// macOS Keychain namespaces credentials by whether CLAUDE_CONFIG_DIR was set
// at login, so checking the default ~/.claude profile with the env var set
// can report logged-out even though a plain `claude` login is active. On a
// clean logged-out answer for the default profile, re-ask with an empty
// profile dir (the runner strips the ambient env var) and let a clean
// logged-in result win. Errors and non-default profiles keep the first
// answer.
func loadAuth(ctx context.Context, r claudecli.Runner, profile config.Profile) (claudecli.AuthStatus, error) {
	auth, err := claudecli.LoadAuthStatus(ctx, r, profile.Path)
	if !profile.IsDefault || err != nil || auth.LoggedIn {
		return auth, err
	}
	if fallback, fbErr := claudecli.LoadAuthStatus(ctx, r, ""); fbErr == nil && fallback.LoggedIn {
		return fallback, nil
	}
	return auth, nil
}

// refreshProfile reloads a profile's plugin data after an action without the
// marketplace refresh: the catalog was fetched moments earlier by the initial
// load, and a network round-trip per action would stall the action loop.
// prevStale carries the last refresh outcome forward.
func refreshProfile(r claudecli.Runner, index, gen int, profile config.Profile, prevStale bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		plugins, latest, err := claudecli.LoadPluginsCached(ctx, r, profile.Path)
		if err != nil {
			return profileErrMsg{index: index, gen: gen, err: err}
		}
		latest.Stale = prevStale
		auth, authErr := loadAuth(ctx, r, profile)
		return profileLoadedMsg{index: index, gen: gen, auth: auth, authErr: authErr,
			plugins: plugins, latest: latest}
	}
}

// Update routes key presses and per-profile load/spinner messages; each load
// or error message touches only its own column's state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Without a width the input never scrolls (bubbles disables its overflow
		// window at Width <= 0), so a query longer than the line renders in full
		// and fitWidth chops off the tail — including the cursor. Leave a cell
		// for the cursor past the value, and never clamp to 0: a terminal too
		// narrow for the prompt would otherwise turn scrolling back off.
		m.filterInput.Width = max(1, m.width-lipgloss.Width(filterPrompt)-1)
		return m, nil

	case profileLoadedMsg:
		col := &m.columns[msg.index]
		if msg.gen != col.gen {
			return m, nil
		}
		col.status = statusLoaded
		col.auth = msg.auth
		col.authErr = msg.authErr
		col.plugins = msg.plugins
		col.latest = msg.latest
		col.err = nil
		return m, nil

	case profileErrMsg:
		col := &m.columns[msg.index]
		if msg.gen != col.gen {
			return m, nil
		}
		col.status = statusError
		col.err = msg.err
		return m, nil

	case mcpLoadedMsg:
		col := &m.columns[msg.index]
		if msg.gen != col.mcpGen {
			return m, nil
		}
		col.mcpStatus = statusLoaded
		col.mcp = msg.servers
		col.mcpErr = nil
		return m, nil

	case mcpErrMsg:
		col := &m.columns[msg.index]
		if msg.gen != col.mcpGen {
			return m, nil
		}
		col.mcpStatus = statusError
		col.mcpErr = msg.err
		return m, nil

	case actionDoneMsg:
		col := &m.columns[msg.index]
		col.busy = false
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("%s %s in %s failed: %v",
				msg.verb, msg.target, col.profile.Label, msg.err), true)
			// A CLI-reported failure changed nothing, so the column's data
			// stays valid. A timed-out action may have (partially) applied
			// before the kill, and a chained action may have written in an
			// earlier step (mutated); those must reload the column.
			if !msg.uncertain && !msg.mutated {
				return m, nil
			}
		} else {
			m.setStatus(fmt.Sprintf("%s %s in %s: done",
				msg.verb, msg.target, col.profile.Label), false)
		}
		col.status = statusLoading
		col.err = nil
		col.gen++
		stale := col.latest.Stale
		// A successful update of the profile's only marketplace refreshed
		// every catalog the stale marker covers; with several marketplaces
		// the others stay unrefreshed, so the conservative flag is kept.
		if msg.err == nil && msg.verb == "update marketplace" && len(col.plugins.Marketplaces) == 1 {
			stale = false
		}
		cmds := []tea.Cmd{
			refreshProfile(m.runner, msg.index, col.gen, col.profile, stale),
			m.spinner.Tick,
		}
		// Plugin actions can add or remove plugin-provided MCP servers
		// (plugin:<plugin>:<name>), so a loaded MCP tab must reload this
		// column too or it keeps showing servers of an uninstalled plugin.
		// The gen bump also supersedes any mcp list that read mid-mutation.
		if m.mcpStarted {
			col.mcpStatus = statusLoading
			col.mcpErr = nil
			col.mcpGen++
			cmds = append(cmds, loadMCPProfile(m.runner, msg.index, col.mcpGen, col.profile.Path))
		}
		return m, tea.Batch(cmds...)

	case marketplaceAddedMsg:
		// A failed add means the install is never attempted; route the failure
		// through the shared actionDoneMsg path so busy-clearing and the
		// uncertain→reload semantics stay in one place.
		if msg.err != nil {
			return m.Update(actionDoneMsg{index: msg.index, verb: "add marketplace", target: msg.name,
				err: msg.err, uncertain: msg.uncertain})
		}
		col := m.columns[msg.index]
		m.setStatus(fmt.Sprintf("install %s in %s…", msg.plugin, col.profile.Label), false)
		install := runPluginAction(m.runner, msg.index, col.profile.Path, msg.plugin, "install")
		// The add already wrote to the profile's config, so the install's
		// result must reload the column even when the install itself fails
		// cleanly — otherwise the new marketplace stays invisible.
		return m, func() tea.Msg {
			done := install().(actionDoneMsg)
			done.mutated = true
			return done
		}

	case mcpActionDoneMsg:
		col := &m.columns[msg.index]
		col.busy = false
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("remove %s in %s failed: %v",
				msg.server, col.profile.Label, msg.err), true)
			// Same split as actionDoneMsg: only a timed-out remove may have
			// mutated the config, so only then reload the column.
			if !msg.uncertain {
				return m, nil
			}
		} else {
			m.setStatus(fmt.Sprintf("remove %s in %s: done",
				msg.server, col.profile.Label), false)
		}
		col.mcpStatus = statusLoading
		col.mcpErr = nil
		col.mcpGen++
		return m, tea.Batch(
			loadMCPProfile(m.runner, msg.index, col.mcpGen, col.profile.Path),
			m.spinner.Tick,
		)

	case spinner.TickMsg:
		// The shared spinner keeps ticking while anything is still loading
		// and dies out otherwise (the load helpers restart it).
		if !m.anyLoading() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	default:
		// The input's own keys can emit commands whose replies come back as
		// messages it must see again — ctrl+v answers with the clipboard's
		// contents. Nothing else reaches this branch (every other message the
		// app raises is cased above), so forwarding while editing is what keeps
		// those keys from silently doing nothing.
		if m.filterEditing {
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.setQuery(m.filterInput.Value())
			return m, cmd
		}
	}
	return m, nil
}

// columnLoading reports whether column i still has a load in flight — plugin
// data, or MCP data once its lazy load has started.
func (m Model) columnLoading(i int) bool {
	return m.columns[i].status == statusLoading ||
		(m.mcpStarted && m.columns[i].mcpStatus == statusLoading)
}

// anyLoading reports whether any column still has a load in flight; it keeps
// the shared spinner alive.
func (m Model) anyLoading() bool {
	for i := range m.columns {
		if m.columnLoading(i) {
			return true
		}
	}
	return false
}

func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The confirmation prompt wins over the filter input: it holds back a
	// destructive action and must resolve before anything else reads a key.
	if m.pending != nil {
		return m.handleConfirmKey(key)
	}
	if m.filterEditing {
		return m.handleFilterKey(key)
	}
	m.setStatus("", false)
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "/":
		return m.openFilter(), nil
	case "esc":
		m.setQuery("")
		return m, nil
	// enterTab mutates scalar Model fields through a pointer receiver, so it
	// must run before the return operand copies m: the Go spec leaves the
	// order of a plain operand vs a call in `return m, m.enterTab()`
	// unspecified (spec "Order of evaluation").
	case "tab":
		m.tab = (m.tab + 1) % tabCount
		cmd := m.enterTab()
		return m, cmd
	case "shift+tab":
		m.tab = (m.tab + tabCount - 1) % tabCount
		cmd := m.enterTab()
		return m, cmd
	case "left", "h":
		m.selCol = max(0, m.selCol-1)
		return m, nil
	case "right", "l":
		m.selCol = min(len(m.columns)-1, m.selCol+1)
		return m, nil
	case "up", "k":
		// Clamp before moving: a reload can shrink the row set under an
		// out-of-range selection, which would otherwise need dead presses
		// to walk back into view.
		m.selRow = max(0, min(m.selRow, m.rowCount()-1)-1)
		return m, nil
	case "down", "j":
		m.selRow = min(max(0, m.rowCount()-1), m.selRow+1)
		return m, nil
	case "r":
		// Reload only the active tab's data: the other tab's data stays valid
		// and MCP reloads are expensive (per-server health checks).
		if m.tab == tabMCP {
			return m, m.reloadMCP()
		}
		return m, m.reloadPlugins()
	case "e", "d", "u", "x", "i":
		if m.tab == tabPlugins {
			return m.startAction(key.String())
		}
		return m.startMCPAction(key.String())
	case "enter", " ":
		return m.toggleFold(), nil
	}
	return m, nil
}

// toggleFold flips the fold state of the selected marketplace group. Plugin
// rows and the MCP tab ignore the key; during a y/n confirmation the key
// never reaches here (handleConfirmKey resolves it first). An active filter
// ignores it too: activeFolds is nil there, so a fold recorded now would be
// invisible until the filter is cleared and would then swallow rows the user
// never folded.
func (m Model) toggleFold() Model {
	if m.tab != tabPlugins || m.filters[tabPlugins] != "" {
		return m
	}
	groups, _ := m.pluginGroups()
	refs := m.visiblePluginRefs(groups)
	if len(refs) == 0 {
		return m
	}
	sel := min(m.selRow, len(refs)-1)
	ref := refs[sel]
	if ref.kind != rowMarketplace {
		return m
	}
	if m.folded == nil {
		m.folded = map[string]bool{}
	}
	name := groups[ref.group].Marketplace.Name
	m.folded[name] = !m.folded[name]
	// Only rows after the toggled header appear or disappear, so the clamped
	// index still addresses that header.
	m.selRow = sel
	return m
}

// enterTab clamps the row selection to the new tab's row count and starts
// the lazy MCP load on the first visit.
func (m *Model) enterTab() tea.Cmd {
	m.selRow = min(m.selRow, max(0, m.rowCount()-1))
	if m.tab == tabMCP && !m.mcpStarted {
		m.mcpStarted = true
		return m.loadMCPAll()
	}
	return nil
}

// openFilter focuses the filter input on the active tab's query, so `/` on an
// already-filtered tab refines that query instead of starting over.
func (m Model) openFilter() Model {
	m.filterEditing = true
	m.filterInput.SetValue(m.filters[m.tab])
	m.filterInput.CursorEnd()
	m.filterInput.Focus()
	return m
}

// closeFilter blurs the input and applies query to the active tab.
func (m Model) closeFilter(query string) Model {
	m.filterEditing = false
	m.filterInput.Blur()
	m.setQuery(query)
	return m
}

// setQuery applies query to the active tab, sending the selection back to the
// top: a new query rebuilds the row set, so the old row index addresses an
// unrelated row (and the views' clamping would silently land on the last one).
// The query is stored normalized, because everything else keys "is a filter
// active" off it being non-empty: a whitespace-only query is empty to the
// filters but would otherwise count as active here, disabling folding and
// spending a chrome line on an indicator for a filter that hides nothing.
func (m *Model) setQuery(query string) {
	query = model.NormalizeQuery(query)
	if m.filters[m.tab] == query {
		return
	}
	m.filters[m.tab] = query
	m.selRow = 0
}

// handleFilterKey routes keys while the filter input is focused: enter applies
// the query, esc drops it, and every other key is typed into the input — so
// the quit, navigation and action keys are unreachable in this mode. ctrl+c
// still quits, and tab still switches tabs (closing the input, keeping the
// query), because neither has a useful meaning as literal text.
func (m Model) handleFilterKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// As in handleKey: the transient status is dismissed by the next key, so an
	// action's result cannot linger under the input and read as a reply to what
	// is being typed.
	m.setStatus("", false)
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeFilter(""), nil
	case "enter":
		return m.closeFilter(m.filterInput.Value()), nil
	case "tab", "shift+tab":
		return m.closeFilter(m.filterInput.Value()).handleKey(key)
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(key)
	m.setQuery(m.filterInput.Value())
	return m, cmd
}

// filterLine renders the slot above the table header: the input while it is
// focused, otherwise — with a query still applied — an indicator carrying the
// query, how many of total rows survive it, and the key that drops it. A filter
// that is active but invisible would read as missing plugins.
//
// counted says whether the caller's counts are drawn from every column. They
// are not while any column loads or errors, because the accessors feeding them
// skip such columns — so a reload under a filter would count 0 of 0 and read as
// "your query matches nothing" over a table of spinners. Same lie the no-match
// line is gated against, so the counts drop out until the numbers are whole.
func (m Model) filterLine(match, total int, counted bool) string {
	if m.filterEditing {
		return m.fitWidth(m.filterInput.View()) + "\n"
	}
	query := m.filters[m.tab]
	if query == "" {
		return ""
	}
	text := fmt.Sprintf("%s%s  esc: clear", filterPrompt, query)
	if counted {
		text = fmt.Sprintf("%s%s (%d/%d)  esc: clear",
			filterPrompt, query, match, total)
	}
	return statusStyle.Render(m.fitWidth(text)) + "\n"
}

// filterVisible reports whether the active tab renders the filter line.
func (m Model) filterVisible() bool {
	return m.filterEditing || m.filters[m.tab] != ""
}

// noMatchLine follows the table when the query matched nothing, so an active
// filter is never mistaken for an empty profile. It is drawn below the table
// rather than in place of it: the table carries the per-column spinners and
// `error:` lines, which an errored profile needs even while its rows are gone.
//
// visible is the filtered row count, total the unfiltered one, and settled
// reports that no column is still loading. Only an empty row set drawn from a
// non-empty one, over columns that are done loading, can mean "the query
// excluded everything" — while a column loads its rows are merely not in yet.
// The gate counts rows, not plugins: a profile with marketplaces but no plugins
// installed still has rows for the query to exclude.
func (m Model) noMatchLine(kind string, visible, total int, settled bool) string {
	if visible > 0 || total == 0 || m.filters[m.tab] == "" || !settled {
		return ""
	}
	text := fmt.Sprintf("no %s match %q", kind, m.filters[m.tab])
	return statusStyle.Render(m.fitWidth(text)) + "\n"
}

// handleConfirmKey resolves the confirmation prompt: y runs the held-back
// action, any other key cancels it — except ctrl+c, which must always quit.
func (m Model) handleConfirmKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+c" {
		return m, tea.Quit
	}
	p := *m.pending
	m.pending = nil
	if key.String() != "y" {
		m.setStatus(p.verb+" cancelled", false)
		return m, nil
	}
	col := m.columns[p.col]
	m.setStatus(fmt.Sprintf("%s %s in %s…", p.verb, p.target, col.profile.Label), false)
	m.columns[p.col].busy = true
	if p.mcp {
		return m, runMCPRemove(m.runner, p.col, col.profile.Path, p.target)
	}
	if p.marketplace {
		return m, runMarketplaceAction(m.runner, p.col, col.profile.Path, "remove", p.target, "")
	}
	return m, runPluginAction(m.runner, p.col, col.profile.Path,
		claudecli.ParsePluginID(p.target), p.verb)
}

// actionVerbs maps an action key to its `claude plugin <verb>` subcommand.
var actionVerbs = map[string]string{
	"e": "enable",
	"d": "disable",
	"u": "update",
	"x": "uninstall",
	"i": "install",
}

// startAction validates the selected cell for the pressed action key and
// either fires the CLI command, or (for uninstall) arms the confirmation
// prompt first.
func (m Model) startAction(key string) (tea.Model, tea.Cmd) {
	verb := actionVerbs[key]
	groups, _ := m.pluginGroups()
	refs := m.visiblePluginRefs(groups)
	if len(refs) == 0 {
		return m, nil
	}
	ref := refs[min(m.selRow, len(refs)-1)]
	// Marketplace header rows carry their own action set, not the plugin one.
	if ref.kind == rowMarketplace {
		return m.startMarketplaceAction(key, groups[ref.group].Marketplace)
	}
	row := groups[ref.group].Plugins[ref.plugin]
	col := m.columns[m.selCol]
	if col.status != statusLoaded {
		m.setStatus(col.profile.Label+" is not loaded yet", true)
		return m, nil
	}
	if col.busy {
		m.setStatus(col.profile.Label+" has an action in progress", true)
		return m, nil
	}
	if !actionAllowed(verb, row.Cells[m.selCol].State) {
		m.setStatus(fmt.Sprintf("cannot %s %s in %s", verb, row.ID, col.profile.Label), true)
		return m, nil
	}
	// Actions pin --scope user (see runPluginAction), which cannot touch a
	// project/local-scope install (cwd-dependent, shown identically in every
	// column) — refuse with a hint instead of surfacing the CLI's raw error.
	if s := row.Cells[m.selCol].Scope; s != "" && s != "user" {
		m.setStatus(fmt.Sprintf("cannot %s %s: installed at %s scope — use `claude plugin` in the owning directory",
			verb, row.ID, s), true)
		return m, nil
	}
	// Plugin ids come from marketplace catalogs (third-party data); refuse
	// anything the claude CLI would parse as a flag instead of a name.
	if strings.HasPrefix(row.ID.String(), "-") {
		m.setStatus(fmt.Sprintf("refusing %s: plugin name looks like a CLI flag", row.ID), true)
		return m, nil
	}
	// Installing needs the plugin's marketplace configured in the target
	// profile; when it is missing and another profile knows a usable source,
	// add the marketplace implicitly instead of refusing.
	if verb == "install" && !hasAvailable(col.plugins, row.ID) {
		return m.startInstallWithAdd(row.ID, groups[ref.group].Marketplace)
	}
	if verb == "uninstall" {
		m.pending = &pendingAction{verb: verb, target: row.ID.String(), col: m.selCol}
		return m, nil
	}
	m.setStatus(fmt.Sprintf("%s %s in %s…", verb, row.ID, col.profile.Label), false)
	m.columns[m.selCol].busy = true
	return m, runPluginAction(m.runner, m.selCol, col.profile.Path, row.ID, verb)
}

// startInstallWithAdd handles install into a profile whose catalogs lack the
// plugin's marketplace: with a usable source the add fires first and the
// install is chained onto its success message (marketplaceAddedMsg); without
// one the install is refused as it was before implicit adds existed.
func (m Model) startInstallWithAdd(id claudecli.PluginID, mkt model.MarketplaceRow) (tea.Model, tea.Cmd) {
	col := m.columns[m.selCol]
	// A failed marketplace list left the profile's configured set unknown;
	// a blind add could duplicate an existing marketplace, so refuse.
	if col.plugins.MarketplacesUnknown {
		m.setStatus(fmt.Sprintf("cannot install %s in %s: marketplace state unknown — reload (r)",
			id, col.profile.Label), true)
		return m, nil
	}
	// The marketplace is configured but its catalog lacks the plugin (stale
	// or diverged clone): adding again would fail as a duplicate, so fire
	// the plain install and let the CLI resolve or report.
	if mkt.Cells[m.selCol].Configured {
		m.setStatus(fmt.Sprintf("install %s in %s…", id, col.profile.Label), false)
		m.columns[m.selCol].busy = true
		return m, runPluginAction(m.runner, m.selCol, col.profile.Path, id, "install")
	}
	if mkt.SourceConflict || mkt.SourceArg == "" {
		m.setStatus(fmt.Sprintf("cannot install %s in %s: marketplace %q is not configured there"+
			" (claude plugin marketplace add)", id, col.profile.Label, mkt.Name), true)
		return m, nil
	}
	// The source is third-party data passed as a positional arg; refuse
	// anything the claude CLI would parse as a flag.
	if strings.HasPrefix(mkt.SourceArg, "-") {
		m.setStatus(fmt.Sprintf("refusing install: marketplace %s source looks like a CLI flag",
			mkt.Name), true)
		return m, nil
	}
	m.setStatus(fmt.Sprintf("adding marketplace %s in %s…", mkt.Name, col.profile.Label), false)
	m.columns[m.selCol].busy = true
	return m, runMarketplaceAddFor(m.runner, m.selCol, col.profile.Path, mkt.Name, mkt.SourceArg, id)
}

// runMarketplaceAddFor fires the implicit `plugin marketplace add` preceding
// a plugin install; unlike runMarketplaceAction it reports through
// marketplaceAddedMsg so the handler can chain the install.
func runMarketplaceAddFor(r claudecli.Runner, index int, profileDir, name, sourceArg string,
	plugin claudecli.PluginID,
) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		_, err := r.Run(ctx, profileDir, "plugin", "marketplace", "add", sourceArg, "--scope", "user")
		return marketplaceAddedMsg{index: index, name: name, plugin: plugin, err: err,
			uncertain: err != nil && ctx.Err() != nil}
	}
}

// marketplaceVerbs maps an action key to its `plugin marketplace <verb>`
// subcommand; enable/disable have no marketplace equivalent, so e/d are
// no-ops on marketplace rows.
var marketplaceVerbs = map[string]string{
	"i": "add",
	"u": "update",
	"x": "remove",
}

// startMarketplaceAction validates the selected marketplace cell for the
// pressed action key and either fires the CLI command, or (for remove) arms
// the confirmation prompt first.
func (m Model) startMarketplaceAction(key string, row model.MarketplaceRow) (tea.Model, tea.Cmd) {
	verb, ok := marketplaceVerbs[key]
	if !ok {
		return m, nil
	}
	col := m.columns[m.selCol]
	if col.status != statusLoaded {
		m.setStatus(col.profile.Label+" is not loaded yet", true)
		return m, nil
	}
	if col.busy {
		m.setStatus(col.profile.Label+" has an action in progress", true)
		return m, nil
	}
	// A failed marketplace list left this profile's configured set unknown:
	// the cell's Configured=false is not trustworthy, so any marketplace
	// action could target the wrong state — refuse them all.
	if col.plugins.MarketplacesUnknown {
		m.setStatus(fmt.Sprintf("cannot %s %s: marketplace state unknown in %s — reload (r)",
			verb, row.Name, col.profile.Label), true)
		return m, nil
	}
	// Marketplace names and sources are third-party data passed as positional
	// args; refuse anything the claude CLI would parse as a flag.
	if strings.HasPrefix(row.Name, "-") || (verb == "add" && strings.HasPrefix(row.SourceArg, "-")) {
		m.setStatus(fmt.Sprintf("refusing %s: marketplace %s looks like a CLI flag",
			verb, row.Name), true)
		return m, nil
	}
	cell := row.Cells[m.selCol]
	switch verb {
	case "add":
		switch {
		case cell.Configured:
			m.setStatus(fmt.Sprintf("marketplace %s is already configured in %s",
				row.Name, col.profile.Label), true)
			return m, nil
		case row.SourceConflict:
			m.setStatus(fmt.Sprintf("cannot add %s: profiles disagree on its source", row.Name), true)
			return m, nil
		case row.SourceArg == "":
			m.setStatus(fmt.Sprintf("cannot add %s: no known source", row.Name), true)
			return m, nil
		}
	default: // update, remove
		if !cell.Configured {
			m.setStatus(fmt.Sprintf("cannot %s %s: not configured in %s",
				verb, row.Name, col.profile.Label), true)
			return m, nil
		}
	}
	// Remove is destructive (it can drop the marketplace's installed plugins),
	// so it is always confirmation-gated, like plugin uninstall.
	if verb == "remove" {
		m.pending = &pendingAction{verb: "remove marketplace", target: row.Name,
			marketplace: true, col: m.selCol}
		return m, nil
	}
	m.setStatus(fmt.Sprintf("%s marketplace %s in %s…", verb, row.Name, col.profile.Label), false)
	m.columns[m.selCol].busy = true
	return m, runMarketplaceAction(m.runner, m.selCol, col.profile.Path, verb, row.Name, row.SourceArg)
}

// runMarketplaceAction fires one `plugin marketplace <verb>` invocation; its
// result reuses actionDoneMsg, so the busy-clearing, refresh, and MCP-reload
// semantics match plugin actions.
func runMarketplaceAction(r claudecli.Runner, index int, profileDir, verb, name, sourceArg string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		var args []string
		switch verb {
		case "add":
			// Scope is pinned to user so the add lands in the profile's own
			// config, matching every other mutation cpm fires.
			args = []string{"plugin", "marketplace", "add", sourceArg, "--scope", "user"}
		case "update":
			// update has no scope flag in the CLI.
			args = []string{"plugin", "marketplace", "update", name}
		case "remove":
			// --scope user is mandatory here: without it the CLI removes the
			// marketplace from ALL scopes, not just this profile's config.
			args = []string{"plugin", "marketplace", "remove", name, "--scope", "user"}
		}
		_, err := r.Run(ctx, profileDir, args...)
		return actionDoneMsg{index: index, verb: verb + " marketplace", target: name, err: err,
			uncertain: err != nil && ctx.Err() != nil}
	}
}

// hasAvailable reports whether the plugin appears in the profile's own
// marketplace catalogs.
func hasAvailable(data claudecli.PluginData, id claudecli.PluginID) bool {
	return slices.ContainsFunc(data.Available, func(a claudecli.AvailablePlugin) bool {
		return a.ID == id
	})
}

// actionAllowed reports whether the verb makes sense for the cell's state:
// enable needs a disabled plugin, disable an enabled one, update/uninstall any
// installed one, and install a profile where the plugin is absent.
func actionAllowed(verb string, state model.CellState) bool {
	switch verb {
	case "enable":
		return state == model.Disabled
	case "disable":
		return state == model.Installed
	case "update", "uninstall":
		return state != model.Absent
	case "install":
		return state == model.Absent
	default:
		return false
	}
}

// startMCPAction handles action keys on the MCP tab: x arms the remove
// confirmation for the selected cell, i explains that add is out of scope
// (per IDEA: MCP add needs cmd/url/args capture — future work), and the
// remaining plugin keys are ignored (MCP has no enable/disable/update).
func (m Model) startMCPAction(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "i":
		m.setStatus("MCP add is not yet supported — use `claude mcp add` directly", false)
		return m, nil
	case "x":
	default:
		return m, nil
	}
	rows := m.mcpRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[min(m.selRow, len(rows)-1)]
	col := m.columns[m.selCol]
	if col.mcpStatus != statusLoaded {
		m.setStatus(col.profile.Label+" is not loaded yet", true)
		return m, nil
	}
	// An in-flight plugin load runs `plugin marketplace update` (a write);
	// `mcp remove` would be a second concurrent writer on the same config dir.
	if col.status == statusLoading {
		m.setStatus(col.profile.Label+" is still loading plugin data", true)
		return m, nil
	}
	if col.busy {
		m.setStatus(col.profile.Label+" has an action in progress", true)
		return m, nil
	}
	if !row.Cells[m.selCol].Present {
		m.setStatus(fmt.Sprintf("cannot remove %s in %s", row.Name, col.profile.Label), true)
		return m, nil
	}
	// Server names come straight from CLI output; refuse anything the
	// claude CLI would parse as a flag instead of a name.
	if strings.HasPrefix(row.Name, "-") {
		m.setStatus(fmt.Sprintf("refusing %s: server name looks like a CLI flag", row.Name), true)
		return m, nil
	}
	// plugin:<plugin>:<name> servers are provided by plugins, not by the
	// profile's MCP config; `claude mcp remove` cannot touch them.
	if strings.HasPrefix(row.Name, "plugin:") {
		m.setStatus(fmt.Sprintf("cannot remove %s: it is provided by a plugin — uninstall the plugin instead",
			row.Name), true)
		return m, nil
	}
	// Remove is destructive (the server's config is not recoverable from CPM),
	// so it is always confirmation-gated.
	m.pending = &pendingAction{verb: "remove", target: row.Name, mcp: true, col: m.selCol}
	return m, nil
}

func runMCPRemove(r claudecli.Runner, index int, profileDir, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		// Scope is pinned to user — the profile's own config. Without it the
		// CLI removes from whichever scope holds the name, so a project/local
		// row (cwd-dependent, shown identically in every column) would be
		// silently deleted from config shared by all profiles.
		_, err := r.Run(ctx, profileDir, "mcp", "remove", "--scope", "user", name)
		return mcpActionDoneMsg{index: index, server: name, err: err,
			uncertain: err != nil && ctx.Err() != nil}
	}
}

func runPluginAction(r claudecli.Runner, index int, profileDir string,
	plugin claudecli.PluginID, verb string,
) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		// Scope is pinned to user — the profile's own config. enable/disable
		// default to auto-detect, so acting on a project/local-scope plugin
		// (cwd-dependent, shown identically in every column) would silently
		// mutate config shared by all profiles.
		_, err := r.Run(ctx, profileDir, "plugin", verb, "--scope", "user", plugin.String())
		return actionDoneMsg{index: index, verb: verb, target: plugin.String(), err: err,
			uncertain: err != nil && ctx.Err() != nil}
	}
}

func (m *Model) setStatus(text string, isErr bool) {
	m.status = text
	m.statusErr = isErr
}

var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Underline(true)
	inactiveTabStyle = lipgloss.NewStyle().Faint(true)
	errStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	labelStyle       = lipgloss.NewStyle().Bold(true)
	pathStyle        = lipgloss.NewStyle().Faint(true)
	absentStyle      = lipgloss.NewStyle().Faint(true)
	outdatedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	statusStyle      = lipgloss.NewStyle().Faint(true)
)

// View renders the tab bar, the active tab's comparison table, and key help.
func (m Model) View() string {
	var b strings.Builder
	for t := range tabCount {
		style := inactiveTabStyle
		if t == m.tab {
			style = activeTabStyle
		}
		if t > 0 {
			b.WriteString("  ")
		}
		b.WriteString(style.Render(t.String()))
	}
	b.WriteString("\n\n")

	switch m.tab {
	case tabPlugins:
		b.WriteString(m.viewPlugins())
	case tabMCP:
		b.WriteString(m.viewMCP())
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	// While the input is focused every other key types a literal rune, so the
	// navigation, action and quit hints would all be lies.
	if m.filterEditing {
		b.WriteString("\nenter: apply  esc: clear\n")
		return b.String()
	}
	b.WriteString("\n←/→/h/l ↑/↓/j/k: select  tab: switch  /: filter  r: reload  q: quit")
	switch {
	case m.tab == tabMCP:
		b.WriteString("\nx: remove")
	case m.selectedMarketplaceRow():
		b.WriteString("\ni: add  u: update  x: remove")
		// Folding is disabled while a filter is applied (see toggleFold).
		if m.filters[tabPlugins] == "" {
			b.WriteString("  enter: fold")
		}
	default:
		b.WriteString("\ne: enable  d: disable  u: update  x: uninstall  i: install")
	}
	b.WriteString("\n")
	return b.String()
}

// selectedMarketplaceRow reports whether the plugins-tab selection sits on a
// marketplace header row; the second footer help line follows the row kind.
func (m Model) selectedMarketplaceRow() bool {
	groups, _ := m.pluginGroups()
	refs := m.visiblePluginRefs(groups)
	if len(refs) == 0 {
		return false
	}
	return refs[min(m.selRow, len(refs)-1)].kind == rowMarketplace
}

// statusLine renders the confirmation prompt when one is pending, otherwise
// the transient status/error text (possibly empty). The text is capped at the
// terminal width: rowWindow budgets exactly one row for this line, so letting
// a long CLI error soft-wrap would push the header chrome off-screen.
func (m Model) statusLine() string {
	if m.pending != nil {
		return m.fitWidth(fmt.Sprintf("%s %s from %s? y/n", m.pending.verb,
			m.pending.target, m.columns[m.pending.col].profile.Label))
	}
	if m.status == "" {
		return ""
	}
	text := m.fitWidth(m.status)
	if m.statusErr {
		return errStyle.Render(text)
	}
	return statusStyle.Render(text)
}

// fitWidth truncates s to the terminal width; a no-op before the first
// WindowSizeMsg arrives.
func (m Model) fitWidth(s string) string {
	if m.width <= 0 {
		return s
	}
	return truncate(s, m.width)
}

// allPluginGroups builds the grouped comparison data (marketplace header rows
// plus their plugin rows) from the currently loaded columns and reports
// whether any latest versions are stale (a marketplace refresh failed, so
// that profile fell back to its cached catalog). The name filter is not
// applied — see pluginGroups.
func (m Model) allPluginGroups() ([]model.PluginGroup, bool) {
	perProfile := make([]claudecli.PluginData, len(m.columns))
	perLatest := make([]claudecli.LatestVersions, len(m.columns))
	for i := range m.columns {
		// A column that failed to (re)load keeps its previous data but renders
		// blank cells; feeding that data into the groups would produce rows
		// with no visible owner.
		if m.columns[i].status != statusLoaded {
			continue
		}
		perProfile[i] = m.columns[i].plugins
		perLatest[i] = m.columns[i].latest
	}
	latest, stale := model.MergeLatestVersions(perLatest)
	groups := model.BuildPluginGroups(perProfile, latest)
	return groups, stale
}

// pluginGroups is allPluginGroups narrowed by the plugins tab's query — the
// choke point every consumer (view, row count, folding, actions) reads.
func (m Model) pluginGroups() ([]model.PluginGroup, bool) {
	groups, stale := m.allPluginGroups()
	return model.FilterPluginGroups(groups, m.filters[tabPlugins]), stale
}

// tabLoading reports whether a column is still loading the tab's data. The
// no-match empty state and the indicator's counts are gated on it: a loading
// column contributes no rows, so with any other column loaded the query would
// be blamed for a table that is merely not filled in yet. An errored column
// contributes no rows either, but it is settled, not transient — waiting on it
// would suppress the empty state and the counts until the profile loads, which
// it may never do.
func (m Model) tabLoading(t tab) bool {
	for i := range m.columns {
		st := m.columns[i].status
		if t == tabMCP {
			st = m.columns[i].mcpStatus
		}
		if st == statusLoading {
			return true
		}
	}
	return false
}

// activeFolds is the fold map in effect for the current render: none while a
// filter is active, since a folded group would hide matching rows. The fold
// state itself is kept, so clearing the filter restores it.
func (m Model) activeFolds() map[string]bool {
	if m.filters[tabPlugins] != "" {
		return nil
	}
	return m.folded
}

// visiblePluginRefs is the single choke point for plugins-tab row visibility:
// the filtered groups, flattened in render order under the active folds.
func (m Model) visiblePluginRefs(groups []model.PluginGroup) []rowRef {
	return visibleRefs(groups, m.activeFolds())
}

func (m Model) viewPlugins() string {
	all, stale := m.allPluginGroups()
	groups := model.FilterPluginGroups(all, m.filters[tabPlugins])
	refs := m.visiblePluginRefs(groups)
	// The indicator counts matched entries, not rows: a marketplace header row
	// the filter keeps only to hold a matching plugin is not itself a match, so
	// counting rows would inflate both numbers. Folds are ignored too, so the
	// count does not move when a group is folded.
	settled := !m.tabLoading(tabPlugins)
	line := m.filterLine(
		model.CountPluginMatches(all, m.filters[tabPlugins]),
		model.CountPluginEntries(all),
		settled,
	)
	selRow := max(0, min(m.selRow, len(refs)-1))
	start, end := m.rowWindow(len(refs))

	table := comparisonTable{
		profiles: make([]tableColumn, len(m.columns)),
		pinned:   pinnedGroupColumn(groups, refs, start, end, selRow-start, stale, m.activeFolds()),
		sel:      m.selCol,
		width:    m.width,
	}
	for i := range m.columns {
		rowSel := -1
		if i == m.selCol {
			rowSel = selRow - start
		}
		table.profiles[i] = m.columns[i].groupColumn(i, groups, refs[start:end], rowSel, m.spinner.View())
	}
	return line + table.render() + m.overflowLine(start, end, len(refs)) +
		m.noMatchLine("plugins", len(refs), len(visibleRefs(all, nil)), settled)
}

// allMCPRows builds the MCP comparison matrix from the currently loaded
// columns, before the tab's filter narrows it.
func (m Model) allMCPRows() []model.MCPRow {
	perProfile := make([][]claudecli.MCPServer, len(m.columns))
	for i := range m.columns {
		// Same as pluginMatrix: an errored column renders blank cells, so its
		// kept data must not generate rows.
		if m.columns[i].mcpStatus != statusLoaded {
			continue
		}
		perProfile[i] = m.columns[i].mcp
	}
	return model.BuildMCPMatrix(perProfile)
}

// mcpRows is allMCPRows narrowed by the MCP tab's query — the choke point every
// consumer (view, row count, remove action) reads.
func (m Model) mcpRows() []model.MCPRow {
	return model.FilterMCPRows(m.allMCPRows(), m.filters[tabMCP])
}

// rowCount is the active tab's number of visible rows; it bounds the row
// selection.
func (m Model) rowCount() int {
	if m.tab == tabMCP {
		return len(m.mcpRows())
	}
	groups, _ := m.pluginGroups()
	return len(m.visiblePluginRefs(groups))
}

func (m Model) viewMCP() string {
	all := m.allMCPRows()
	rows := model.FilterMCPRows(all, m.filters[tabMCP])
	settled := !m.tabLoading(tabMCP)
	line := m.filterLine(len(rows), len(all), settled)
	selRow := max(0, min(m.selRow, len(rows)-1))
	start, end := m.rowWindow(len(rows))

	table := comparisonTable{
		profiles: make([]tableColumn, len(m.columns)),
		pinned:   pinnedMCPColumn(rows, start, end, selRow-start),
		sel:      m.selCol,
		width:    m.width,
	}
	for i := range m.columns {
		rowSel := -1
		if i == m.selCol {
			rowSel = selRow - start
		}
		table.profiles[i] = m.columns[i].mcpColumn(i, rows[start:end], rowSel, m.spinner.View())
	}
	return line + table.render() + m.overflowLine(start, end, len(rows)) +
		m.noMatchLine("MCP servers", len(rows), len(all), settled)
}

// rowWindow bounds the matrix rows rendered so the table fits the terminal
// height with the selected row always visible; rows scroll under the fixed
// headers.
func (m Model) rowWindow(total int) (start, end int) {
	capacity := total
	if m.height > 0 {
		capacity = max(1, m.height-m.chromeLines())
	}
	if capacity >= total {
		return 0, total
	}
	sel := min(m.selRow, total-1)
	start = min(max(0, sel-capacity+1), total-capacity)
	return start, start + capacity
}

// chromeLines counts the lines the body must leave to the rest of the view;
// an undercount silently scrolls the header chrome off-screen.
func (m Model) chromeLines() int {
	// Tab bar and blank line, three header lines, separator, trailing blank,
	// status line, two help lines, and the overflow marker.
	lines := 11
	if m.filterVisible() {
		lines++
	}
	if m.filterEditing {
		// The action help line is suppressed while the input is focused.
		lines--
	}
	return lines
}

// overflowLine marks rows hidden by the vertical window.
func (m Model) overflowLine(start, end, total int) string {
	if start == 0 && end == total {
		return ""
	}
	return statusStyle.Render(fmt.Sprintf("… rows %d–%d of %d", start+1, end, total)) + "\n"
}

// groupColumn is this profile's table column: a three-line header
// (label, path, account or load status) plus one cell per visible row —
// commit info for marketplace headers, plugin state for plugin rows.
// selRow marks the selected cell (-1 when the selection is elsewhere);
// spin is the shared spinner frame for loading cells.
func (c *column) groupColumn(idx int, groups []model.PluginGroup, refs []rowRef,
	selRow int, spin string,
) tableColumn {
	labelCell := tableCell{text: c.profile.Label, style: labelStyle}
	if selRow >= 0 {
		labelCell.style = labelStyle.Underline(true)
	}
	col := tableColumn{
		header: []tableCell{
			labelCell,
			{text: c.profile.Path, style: pathStyle},
			c.statusCell(spin),
		},
		cells: make([]tableCell, len(refs)),
	}
	for i, ref := range refs {
		var cell tableCell
		if ref.kind == rowMarketplace {
			cell = c.marketplaceCell(groups[ref.group].Marketplace.Cells[idx])
		} else {
			cell = c.bodyCell(groups[ref.group].Plugins[ref.plugin].Cells[idx])
		}
		if i == selRow {
			cell.style = cell.style.Reverse(true)
		}
		col.cells[i] = cell
	}
	return col
}

// marketplaceCell renders one marketplace-header cell: the clone's commit
// `hash date` when known, `local` for a directory source without git info,
// `—` when the marketplace is not configured in this profile. Blank while
// the column's data has not arrived, when the profile's marketplace list
// failed (Configured then reflects nothing), or when a non-directory
// source's git lookup failed (blank means "unknown", which `local` or `—`
// would misstate).
func (c *column) marketplaceCell(cell model.MarketplaceCell) tableCell {
	if c.status != statusLoaded || c.plugins.MarketplacesUnknown {
		return tableCell{}
	}
	switch {
	case !cell.Configured:
		return tableCell{text: "—", style: absentStyle}
	case cell.CommitHash != "":
		return tableCell{text: strings.TrimSpace(cell.CommitHash + " " + cell.CommitDate)}
	case cell.Local:
		return tableCell{text: "local", style: pathStyle}
	default:
		return tableCell{}
	}
}

// mcpColumn is this profile's MCP-tab column: the same three-line header as
// the plugins tab (with the MCP load state on the third line) plus one cell
// per server row. selRow marks the selected cell (-1 when elsewhere); spin
// is the shared spinner frame for loading cells.
func (c *column) mcpColumn(idx int, rows []model.MCPRow, selRow int, spin string) tableColumn {
	labelCell := tableCell{text: c.profile.Label, style: labelStyle}
	if selRow >= 0 {
		labelCell.style = labelStyle.Underline(true)
	}
	col := tableColumn{
		header: []tableCell{
			labelCell,
			{text: c.profile.Path, style: pathStyle},
			c.mcpStatusCell(spin),
		},
		cells: make([]tableCell, len(rows)),
	}
	for i, row := range rows {
		cell := c.mcpBodyCell(row.Cells[idx])
		if i == selRow {
			cell.style = cell.style.Reverse(true)
		}
		col.cells[i] = cell
	}
	return col
}

// mcpStatusCell shows the MCP load state while it is in flight (mcp list is
// slow), then falls back to the shared account line.
func (c *column) mcpStatusCell(spin string) tableCell {
	switch c.mcpStatus {
	case statusLoaded:
		return c.statusCell(spin)
	case statusError:
		return tableCell{text: "error: " + c.mcpErr.Error(), style: errStyle}
	default:
		return tableCell{text: spin + " loading…"}
	}
}

// mcpBodyCell renders one MCP matrix cell: the server's target when present,
// `—` when absent; blank until the column's MCP data has arrived.
func (c *column) mcpBodyCell(cell model.MCPCell) tableCell {
	if c.mcpStatus != statusLoaded {
		return tableCell{}
	}
	if !cell.Present {
		return tableCell{text: "—", style: absentStyle}
	}
	return tableCell{text: cell.Target}
}

// statusCell is the third header line: the account while loaded, otherwise
// the column's load state (spinner or error).
func (c *column) statusCell(spin string) tableCell {
	switch c.status {
	case statusLoaded:
		if c.authErr != nil {
			return tableCell{}
		}
		if !c.auth.LoggedIn {
			return tableCell{text: "not logged in", style: pathStyle}
		}
		var parts []string
		if c.auth.Email != "" {
			parts = append(parts, c.auth.Email)
		}
		if c.auth.SubscriptionType != "" {
			parts = append(parts, c.auth.SubscriptionType)
		}
		if len(parts) == 0 {
			return tableCell{text: "logged in"}
		}
		return tableCell{text: strings.Join(parts, " · ")}
	case statusError:
		return tableCell{text: "error: " + c.err.Error(), style: errStyle}
	default:
		return tableCell{text: spin + " loading…"}
	}
}

// bodyCell renders one matrix cell for this column; cells stay blank until
// the column's data has arrived.
func (c *column) bodyCell(cell model.PluginCell) tableCell {
	if c.status != statusLoaded {
		return tableCell{}
	}
	text := formatPluginCell(cell)
	switch {
	case cell.Outdated:
		return tableCell{text: text, style: outdatedStyle}
	case cell.State == model.Absent:
		return tableCell{text: text, style: absentStyle}
	default:
		return tableCell{text: text}
	}
}

// formatPluginCell renders a plugin's state in one profile: `vX.Y.Z`,
// `disabled (vX.Y.Z)`, or `—`; outdated versions carry a `↑` marker.
func formatPluginCell(c model.PluginCell) string {
	var text string
	switch c.State {
	case model.Absent:
		return "—"
	case model.Disabled:
		text = "disabled"
		if c.Version != "" {
			text = "disabled (" + versionText(c.Version) + ")"
		}
	case model.Installed:
		text = "installed" // version reported as unknown
		if c.Version != "" {
			text = versionText(c.Version)
		}
	}
	if c.Outdated {
		text += " ↑"
	}
	return text
}

// NerdFont chevrons marking a marketplace group's fold state; terminals
// without a NerdFont show tofu — accepted trade-off per the design.
const (
	chevronUnfolded = "" // fa-angle-down
	chevronFolded   = "" // fa-angle-right
)

// pinnedGroupColumn is the identity column of the grouped plugins view:
// chevron-prefixed marketplace headers and indented plugin names with the
// latest available version left-aligned in a sub-column. Cells cover the
// vertical window [start, end) but widths come from all rows so the column
// does not jump while scrolling. sel is the window-relative selected row
// (-1 when none): its cell renders reversed so the selected row stays
// findable on wide tables. stale marks the versions as possibly outdated
// (a marketplace refresh failed, so at least one profile fell back to its
// cached catalog).
func pinnedGroupColumn(groups []model.PluginGroup, refs []rowRef, start, end, sel int,
	stale bool, folded map[string]bool,
) tableColumn {
	const title = "marketplace / plugin"
	texts := make([]string, len(refs))
	idW := lipgloss.Width(title)
	for i, ref := range refs {
		texts[i] = pinnedRowText(groups, ref, folded)
		idW = max(idW, lipgloss.Width(texts[i]))
	}

	latestTitle := "latest"
	if stale {
		latestTitle = "latest (stale)"
	}
	col := tableColumn{
		// Two blank lines align the title with the last profile-header line.
		header: []tableCell{{}, {}, {
			text:  padRight(title, idW) + "  " + latestTitle,
			style: labelStyle,
		}},
		cells: make([]tableCell, 0, end-start),
	}
	for i := start; i < end; i++ {
		ref := refs[i]
		cell := tableCell{text: texts[i], style: labelStyle}
		if ref.kind == rowPlugin {
			latest := groups[ref.group].Plugins[ref.plugin].LatestVersion
			text := padRight(texts[i], idW) + "  " + versionText(latest)
			cell = tableCell{text: strings.TrimRight(text, " ")}
		}
		if i-start == sel {
			cell.style = cell.style.Reverse(true)
		}
		col.cells = append(col.cells, cell)
	}
	return col
}

// pinnedRowText renders one identity cell: a chevron-prefixed marketplace
// header (carrying the count of the plugins a fold or a filter hides) or an
// indented plugin name — the group header carries the marketplace, so plugin
// names drop the `@marketplace` suffix.
func pinnedRowText(groups []model.PluginGroup, ref rowRef, folded map[string]bool) string {
	if ref.kind == rowPlugin {
		return "  " + groups[ref.group].Plugins[ref.plugin].ID.Name
	}
	g := groups[ref.group]
	if folded[g.Marketplace.Name] {
		noun := "plugins"
		if len(g.Plugins) == 1 {
			noun = "plugin"
		}
		return fmt.Sprintf("%s %s (%d %s)", chevronFolded, g.Marketplace.Name, len(g.Plugins), noun)
	}
	// A filter shows only the matching plugins of a group, but the header's
	// actions — remove above all, which can drop the marketplace's installed
	// plugins — still hit the whole marketplace. Name what the filter hides, or
	// a group narrowed to one row reads as a marketplace holding one plugin.
	if g.HiddenPlugins > 0 {
		return fmt.Sprintf("%s %s (+%d hidden)", chevronUnfolded, g.Marketplace.Name, g.HiddenPlugins)
	}
	return chevronUnfolded + " " + g.Marketplace.Name
}

// pinnedMCPColumn is the MCP identity column: the server name. Cells cover
// the vertical window [start, end) but the header is padded to the widest of
// all rows so the column width does not jump while scrolling. sel is the
// window-relative selected row (-1 when none): its cell renders reversed so
// the selected row stays findable on wide tables.
func pinnedMCPColumn(rows []model.MCPRow, start, end, sel int) tableColumn {
	const title = "mcp server"
	nameW := lipgloss.Width(title)
	for _, row := range rows {
		nameW = max(nameW, lipgloss.Width(row.Name))
	}
	col := tableColumn{
		// Two blank lines align the title with the last profile-header line.
		header: []tableCell{{}, {}, {text: padRight(title, nameW), style: labelStyle}},
		cells:  make([]tableCell, 0, end-start),
	}
	for i, row := range rows[start:end] {
		cell := tableCell{text: row.Name}
		if i == sel {
			cell.style = cell.style.Reverse(true)
		}
		col.cells = append(col.cells, cell)
	}
	return col
}

// versionText normalizes a version for display with a single leading "v";
// unknown (empty) versions stay empty.
func versionText(v string) string {
	if v == "" {
		return ""
	}
	return "v" + strings.TrimPrefix(v, "v")
}
