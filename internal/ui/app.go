// Package ui holds the Bubble Tea models and views: the root tabbed app and
// the per-profile comparison tables.
package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/model"
)

// cmdTimeout bounds each UI-fired command (one shared context per tea.Cmd, so
// a load's whole CLI sequence draws from one budget): marketplace update hits
// the network and mcp list health-checks every server, so a hung CLI must
// degrade to the column's error state instead of spinning forever.
const cmdTimeout = 2 * time.Minute

type tab int

const (
	tabPlugins tab = iota
	tabMCP
	tabCount
)

func (t tab) String() string {
	switch t {
	case tabPlugins:
		return "Plugins"
	case tabMCP:
		return "MCP"
	default:
		return fmt.Sprintf("tab(%d)", int(t))
	}
}

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
	// current is dropped, so a superseded slow load cannot overwrite newer
	// data (e.g. a pre-action reload landing after the post-action refresh).
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
	// pending is a destructive action awaiting y/n confirmation.
	pending *pendingAction
	// status is the transient status/error line; cleared on the next key.
	status    string
	statusErr bool
}

// pendingAction is an action held back behind the confirmation prompt.
// Exactly one target is set: plugin for plugin actions, server for MCP
// removes.
type pendingAction struct {
	verb   string
	plugin claudecli.PluginID
	server string
	col    int
}

// target is the pending action's subject as shown in the prompt.
func (p pendingAction) target() string {
	if p.server != "" {
		return p.server
	}
	return p.plugin.String()
}

// New builds the root model for the given profiles. All columns start in the
// loading state; Init fires the loads.
func New(r claudecli.Runner, profiles []config.Profile) Model {
	columns := make([]column, len(profiles))
	for i, p := range profiles {
		columns[i] = column{profile: p}
	}
	return Model{
		runner:  r,
		columns: columns,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
}

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

// actionDoneMsg reports a finished plugin action against one profile.
// uncertain marks a timed-out action: the CLI was killed mid-flight, so the
// write may have (partially) applied and the column data cannot be trusted.
type actionDoneMsg struct {
	index     int
	verb      string
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
		cmds = append(cmds, loadProfile(m.runner, i, m.columns[i].gen, m.columns[i].profile.Path))
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
		cmds = append(cmds, loadProfile(m.runner, i, col.gen, col.profile.Path))
	}
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

// loadMCPAll fans out one MCP load per profile plus the spinner tick; used
// on the first view of the MCP tab and on reload.
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

func loadProfile(r claudecli.Runner, index, gen int, profileDir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		// The fresh load re-fetches the profile's marketplaces so the pinned
		// latest versions never come from a stale cache (user requirement).
		plugins, latest, err := claudecli.LoadPluginsFresh(ctx, r, profileDir)
		if err != nil {
			return profileErrMsg{index: index, gen: gen, err: err}
		}
		// A failed auth read degrades to a blank header instead of failing
		// the whole column. (A logged-out profile is not a failure: the CLI
		// still prints parseable JSON with loggedIn=false.)
		auth, authErr := claudecli.LoadAuthStatus(ctx, r, profileDir)
		return profileLoadedMsg{index: index, gen: gen, auth: auth, authErr: authErr,
			plugins: plugins, latest: latest}
	}
}

// refreshProfile reloads a profile's plugin data after an action without the
// marketplace refresh: the catalog was fetched moments earlier by the initial
// load, and a network round-trip per action would stall the action loop.
// prevStale carries the last refresh outcome forward.
func refreshProfile(r claudecli.Runner, index, gen int, profileDir string, prevStale bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		plugins, latest, err := claudecli.LoadPluginsCached(ctx, r, profileDir)
		if err != nil {
			return profileErrMsg{index: index, gen: gen, err: err}
		}
		latest.Stale = prevStale
		auth, authErr := claudecli.LoadAuthStatus(ctx, r, profileDir)
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
				msg.verb, msg.plugin, col.profile.Label, msg.err), true)
			// A CLI-reported failure changed nothing, so the column's data
			// stays valid. A timed-out action may have (partially) applied
			// before the kill, so the column must be reloaded.
			if !msg.uncertain {
				return m, nil
			}
		} else {
			m.setStatus(fmt.Sprintf("%s %s in %s: done",
				msg.verb, msg.plugin, col.profile.Label), false)
		}
		col.status = statusLoading
		col.err = nil
		col.gen++
		cmds := []tea.Cmd{
			refreshProfile(m.runner, msg.index, col.gen, col.profile.Path, col.latest.Stale),
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
	if m.pending != nil {
		return m.handleConfirmKey(key)
	}
	m.setStatus("", false)
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.tab = (m.tab + 1) % tabCount
		return m, m.enterTab()
	case "shift+tab":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, m.enterTab()
	case "left":
		m.selCol = max(0, m.selCol-1)
		return m, nil
	case "right":
		m.selCol = min(len(m.columns)-1, m.selCol+1)
		return m, nil
	case "up":
		// Clamp before moving: a reload can shrink the row set under an
		// out-of-range selection, which would otherwise need dead presses
		// to walk back into view.
		m.selRow = max(0, min(m.selRow, m.rowCount()-1)-1)
		return m, nil
	case "down":
		m.selRow = min(max(0, m.rowCount()-1), m.selRow+1)
		return m, nil
	case "r":
		// Reload only the active tab's data: the other tab's data stays valid
		// and MCP reloads are expensive (per-server health checks). The MCP
		// reload is not gated on busy: `mcp list` is read-only, so it cannot
		// become a second writer, and the post-action reload supersedes it
		// via the gen stamp anyway.
		if m.tab == tabMCP {
			for i := range m.columns {
				m.columns[i].mcpStatus = statusLoading
				m.columns[i].mcpErr = nil
			}
			return m, m.loadMCPAll()
		}
		return m, m.reloadPlugins()
	case "e", "d", "u", "x", "i":
		if m.tab == tabPlugins {
			return m.startAction(key.String())
		}
		return m.startMCPAction(key.String())
	}
	return m, nil
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
	m.setStatus(fmt.Sprintf("%s %s in %s…", p.verb, p.target(), col.profile.Label), false)
	m.columns[p.col].busy = true
	if p.server != "" {
		return m, runMCPRemove(m.runner, p.col, col.profile.Path, p.server)
	}
	return m, runPluginAction(m.runner, p.col, col.profile.Path, p.plugin, p.verb)
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
	rows := m.pluginRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[min(m.selRow, len(rows)-1)]
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
	// Plugin ids come from marketplace catalogs (third-party data); refuse
	// anything the claude CLI would parse as a flag instead of a name.
	if strings.HasPrefix(row.ID.String(), "-") {
		m.setStatus(fmt.Sprintf("refusing %s: plugin name looks like a CLI flag", row.ID), true)
		return m, nil
	}
	// Installing needs the plugin's marketplace configured in the target
	// profile; without it the CLI would fail with a raw lookup error.
	if verb == "install" && !hasAvailable(col.plugins, row.ID) {
		m.setStatus(fmt.Sprintf("cannot install %s in %s: marketplace %q is not configured there"+
			" (claude plugin marketplace add)", row.ID, col.profile.Label, row.ID.Marketplace), true)
		return m, nil
	}
	if verb == "uninstall" {
		m.pending = &pendingAction{verb: verb, plugin: row.ID, col: m.selCol}
		return m, nil
	}
	m.setStatus(fmt.Sprintf("%s %s in %s…", verb, row.ID, col.profile.Label), false)
	m.columns[m.selCol].busy = true
	return m, runPluginAction(m.runner, m.selCol, col.profile.Path, row.ID, verb)
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
	m.pending = &pendingAction{verb: "remove", server: row.Name, col: m.selCol}
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
		return actionDoneMsg{index: index, verb: verb, plugin: plugin, err: err,
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
	b.WriteString("\n←/→ ↑/↓: select  tab: switch  r: reload  q: quit")
	if m.tab == tabPlugins {
		b.WriteString("\ne: enable  d: disable  u: update  x: uninstall  i: install")
	} else {
		b.WriteString("\nx: remove")
	}
	b.WriteString("\n")
	return b.String()
}

// statusLine renders the confirmation prompt when one is pending, otherwise
// the transient status/error text (possibly empty). The text is capped at the
// terminal width: rowWindow budgets exactly one row for this line, so letting
// a long CLI error soft-wrap would push the header chrome off-screen.
func (m Model) statusLine() string {
	if m.pending != nil {
		return m.fitWidth(fmt.Sprintf("%s %s from %s? y/n", m.pending.verb,
			m.pending.target(), m.columns[m.pending.col].profile.Label))
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

// pluginRows builds the comparison matrix from the currently loaded columns;
// it backs both the rendered table and action-key validation.
func (m Model) pluginRows() []model.PluginRow {
	rows, _ := m.pluginMatrix()
	return rows
}

// pluginMatrix merges each column's freshly resolved latest versions into the
// comparison rows and reports whether any of them are stale (a marketplace
// refresh failed, so that profile fell back to its cached catalog).
func (m Model) pluginMatrix() ([]model.PluginRow, bool) {
	perProfile := make([]claudecli.PluginData, len(m.columns))
	perLatest := make([]claudecli.LatestVersions, len(m.columns))
	for i := range m.columns {
		// A column that failed to (re)load keeps its previous data but renders
		// blank cells; feeding that data into the matrix would produce rows
		// with no visible owner.
		if m.columns[i].status != statusLoaded {
			continue
		}
		perProfile[i] = m.columns[i].plugins
		perLatest[i] = m.columns[i].latest
	}
	latest, stale := model.MergeLatestVersions(perLatest)
	return model.BuildPluginMatrix(perProfile, latest), stale
}

func (m Model) viewPlugins() string {
	rows, stale := m.pluginMatrix()
	selRow := max(0, min(m.selRow, len(rows)-1))
	start, end := m.rowWindow(len(rows))

	table := comparisonTable{
		profiles: make([]tableColumn, len(m.columns)),
		pinned:   pinnedPluginColumn(rows, start, end, stale),
		sel:      m.selCol,
		width:    m.width,
	}
	for i := range m.columns {
		rowSel := -1
		if i == m.selCol {
			rowSel = selRow - start
		}
		table.profiles[i] = m.columns[i].pluginColumn(i, rows[start:end], rowSel, m.spinner.View())
	}
	return table.render() + m.overflowLine(start, end, len(rows))
}

// mcpRows builds the MCP comparison matrix from the currently loaded columns.
func (m Model) mcpRows() []model.MCPRow {
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

// rowCount is the active tab's number of matrix rows; it bounds the row
// selection.
func (m Model) rowCount() int {
	if m.tab == tabMCP {
		return len(m.mcpRows())
	}
	return len(m.pluginRows())
}

func (m Model) viewMCP() string {
	rows := m.mcpRows()
	selRow := max(0, min(m.selRow, len(rows)-1))
	start, end := m.rowWindow(len(rows))

	table := comparisonTable{
		profiles: make([]tableColumn, len(m.columns)),
		pinned:   pinnedMCPColumn(rows, start, end),
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
	return table.render() + m.overflowLine(start, end, len(rows))
}

// rowWindow bounds the matrix rows rendered so the table fits the terminal
// height with the selected row always visible; rows scroll under the fixed
// headers.
func (m Model) rowWindow(total int) (start, end int) {
	capacity := total
	if m.height > 0 {
		// Fixed chrome around the body: tab bar and blank line, three
		// header lines, separator, trailing blank, status line, two help
		// lines, and the overflow marker.
		const chrome = 11
		capacity = max(1, m.height-chrome)
	}
	if capacity >= total {
		return 0, total
	}
	sel := min(m.selRow, total-1)
	start = min(max(0, sel-capacity+1), total-capacity)
	return start, start + capacity
}

// overflowLine marks rows hidden by the vertical window.
func (m Model) overflowLine(start, end, total int) string {
	if start == 0 && end == total {
		return ""
	}
	return statusStyle.Render(fmt.Sprintf("… rows %d–%d of %d", start+1, end, total)) + "\n"
}

// pluginColumn is this profile's table column: a three-line header
// (label, path, account or load status) plus one cell per matrix row.
// selRow marks the selected cell (-1 when the selection is elsewhere);
// spin is the shared spinner frame for loading cells.
func (c *column) pluginColumn(idx int, rows []model.PluginRow, selRow int, spin string) tableColumn {
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
		cells: make([]tableCell, len(rows)),
	}
	for i, row := range rows {
		cell := c.bodyCell(row.Cells[idx])
		if i == selRow {
			cell.style = cell.style.Reverse(true)
		}
		col.cells[i] = cell
	}
	return col
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

// pinnedPluginColumn is the identity column: `name@marketplace` plus the
// latest available version, with the versions left-aligned in a sub-column.
// Cells cover the vertical window [start, end) but the sub-column width comes
// from all rows so it does not jump while scrolling. stale marks the versions
// as possibly outdated (a marketplace refresh failed, so at least one profile
// fell back to its cached catalog).
func pinnedPluginColumn(rows []model.PluginRow, start, end int, stale bool) tableColumn {
	const title = "plugin@marketplace"
	idW := lipgloss.Width(title)
	for _, row := range rows {
		idW = max(idW, lipgloss.Width(row.ID.String()))
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
	for _, row := range rows[start:end] {
		text := padRight(row.ID.String(), idW) + "  " + versionText(row.LatestVersion)
		col.cells = append(col.cells, tableCell{text: strings.TrimRight(text, " ")})
	}
	return col
}

// pinnedMCPColumn is the MCP identity column: the server name. Cells cover
// the vertical window [start, end) but the header is padded to the widest of
// all rows so the column width does not jump while scrolling.
func pinnedMCPColumn(rows []model.MCPRow, start, end int) tableColumn {
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
	for _, row := range rows[start:end] {
		col.cells = append(col.cells, tableCell{text: row.Name})
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
