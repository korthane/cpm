// Package ui holds the Bubble Tea models and views: the root tabbed app and
// the per-profile comparison tables.
package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/model"
)

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
	auth    claudecli.AuthStatus
	plugins claudecli.PluginData
	err     error
	spinner spinner.Model
}

// Model is the root Bubble Tea model: tab state plus one column per profile.
type Model struct {
	runner  claudecli.Runner
	tab     tab
	columns []column
	// selRow / selCol address the selected matrix cell; the view scrolls
	// horizontally so the selected profile column stays visible.
	selRow int
	selCol int
	width  int
	// pending is a destructive action awaiting y/n confirmation.
	pending *pendingAction
	// status is the transient status/error line; cleared on the next key.
	status    string
	statusErr bool
}

// pendingAction is an action held back behind the confirmation prompt.
type pendingAction struct {
	verb   string
	plugin claudecli.PluginID
	col    int
}

// New builds the root model for the given profiles. All columns start in the
// loading state; Init fires the loads.
func New(r claudecli.Runner, profiles []config.Profile) Model {
	columns := make([]column, len(profiles))
	for i, p := range profiles {
		columns[i] = column{
			profile: p,
			spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		}
	}
	return Model{runner: r, columns: columns}
}

// profileLoadedMsg delivers one profile's data; index addresses the column.
type profileLoadedMsg struct {
	index   int
	auth    claudecli.AuthStatus
	plugins claudecli.PluginData
}

// profileErrMsg reports a failed profile load.
type profileErrMsg struct {
	index int
	err   error
}

// actionDoneMsg reports a finished plugin action against one profile.
type actionDoneMsg struct {
	index  int
	verb   string
	plugin claudecli.PluginID
	err    error
}

// Init fans out one load command per profile (they run in parallel) plus each
// column's spinner tick.
func (m Model) Init() tea.Cmd { return m.loadAll() }

func (m Model) loadAll() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2*len(m.columns))
	for i := range m.columns {
		cmds = append(cmds, loadProfile(m.runner, i, m.columns[i].profile.Path))
		cmds = append(cmds, m.columns[i].spinner.Tick)
	}
	return tea.Batch(cmds...)
}

func loadProfile(r claudecli.Runner, index int, profileDir string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		plugins, err := claudecli.LoadPlugins(ctx, r, profileDir)
		if err != nil {
			return profileErrMsg{index: index, err: err}
		}
		// A failed auth read (e.g. logged-out profile) degrades to a blank
		// header instead of failing the whole column.
		auth, _ := claudecli.LoadAuthStatus(ctx, r, profileDir)
		return profileLoadedMsg{index: index, auth: auth, plugins: plugins}
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
		return m, nil

	case profileLoadedMsg:
		col := &m.columns[msg.index]
		col.status = statusLoaded
		col.auth = msg.auth
		col.plugins = msg.plugins
		col.err = nil
		return m, nil

	case profileErrMsg:
		col := &m.columns[msg.index]
		col.status = statusError
		col.err = msg.err
		return m, nil

	case actionDoneMsg:
		col := &m.columns[msg.index]
		if msg.err != nil {
			// The action changed nothing, so the column's data stays valid.
			m.setStatus(fmt.Sprintf("%s %s in %s failed: %v",
				msg.verb, msg.plugin, col.profile.Label, msg.err), true)
			return m, nil
		}
		m.setStatus(fmt.Sprintf("%s %s in %s: done",
			msg.verb, msg.plugin, col.profile.Label), false)
		col.status = statusLoading
		col.err = nil
		return m, tea.Batch(
			loadProfile(m.runner, msg.index, col.profile.Path),
			col.spinner.Tick,
		)

	case spinner.TickMsg:
		// Each spinner only reacts to its own tick (matched by ID), so
		// forwarding to loading columns keeps exactly their ticks alive.
		var cmds []tea.Cmd
		for i := range m.columns {
			if m.columns[i].status != statusLoading {
				continue
			}
			var cmd tea.Cmd
			m.columns[i].spinner, cmd = m.columns[i].spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pending != nil {
		return m.handleConfirmKey(key)
	}
	m.setStatus("", false)
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "left":
		m.selCol = max(0, m.selCol-1)
		return m, nil
	case "right":
		m.selCol = min(len(m.columns)-1, m.selCol+1)
		return m, nil
	case "up":
		m.selRow = max(0, m.selRow-1)
		return m, nil
	case "down":
		m.selRow = min(max(0, len(m.pluginRows())-1), m.selRow+1)
		return m, nil
	case "r":
		for i := range m.columns {
			m.columns[i].status = statusLoading
			m.columns[i].err = nil
		}
		return m, m.loadAll()
	case "e", "d", "u", "x", "i":
		if m.tab == tabPlugins {
			return m.startAction(key.String())
		}
		return m, nil
	}
	return m, nil
}

// handleConfirmKey resolves the confirmation prompt: y runs the held-back
// action, any other key cancels it.
func (m Model) handleConfirmKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := *m.pending
	m.pending = nil
	if key.String() != "y" {
		m.setStatus(p.verb+" cancelled", false)
		return m, nil
	}
	col := m.columns[p.col]
	m.setStatus(fmt.Sprintf("%s %s in %s…", p.verb, p.plugin, col.profile.Label), false)
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
	if !actionAllowed(verb, row.Cells[m.selCol].State) {
		m.setStatus(fmt.Sprintf("cannot %s %s in %s", verb, row.ID, col.profile.Label), true)
		return m, nil
	}
	if verb == "uninstall" {
		m.pending = &pendingAction{verb: verb, plugin: row.ID, col: m.selCol}
		return m, nil
	}
	m.setStatus(fmt.Sprintf("%s %s in %s…", verb, row.ID, col.profile.Label), false)
	return m, runPluginAction(m.runner, m.selCol, col.profile.Path, row.ID, verb)
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

func runPluginAction(r claudecli.Runner, index int, profileDir string,
	plugin claudecli.PluginID, verb string,
) tea.Cmd {
	return func() tea.Msg {
		_, err := r.Run(context.Background(), profileDir, "plugin", verb, plugin.String())
		return actionDoneMsg{index: index, verb: verb, plugin: plugin, err: err}
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
		b.WriteString("MCP servers — coming in a later task\n")
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n←/→ ↑/↓: select  tab: switch  r: reload  q: quit")
	if m.tab == tabPlugins {
		b.WriteString("\ne: enable  d: disable  u: update  x: uninstall  i: install")
	}
	b.WriteString("\n")
	return b.String()
}

// statusLine renders the confirmation prompt when one is pending, otherwise
// the transient status/error text (possibly empty).
func (m Model) statusLine() string {
	if m.pending != nil {
		return fmt.Sprintf("%s %s from %s? y/n", m.pending.verb, m.pending.plugin,
			m.columns[m.pending.col].profile.Label)
	}
	if m.status == "" {
		return ""
	}
	if m.statusErr {
		return errStyle.Render(m.status)
	}
	return statusStyle.Render(m.status)
}

// pluginRows builds the comparison matrix from the currently loaded columns;
// it backs both the rendered table and action-key validation.
func (m Model) pluginRows() []model.PluginRow {
	perProfile := make([]claudecli.PluginData, len(m.columns))
	for i := range m.columns {
		perProfile[i] = m.columns[i].plugins
	}
	return model.BuildPluginMatrix(perProfile, model.LatestVersions(perProfile))
}

func (m Model) viewPlugins() string {
	rows := m.pluginRows()

	table := comparisonTable{
		profiles: make([]tableColumn, len(m.columns)),
		pinned:   pinnedPluginColumn(rows),
		sel:      m.selCol,
		width:    m.width,
	}
	selRow := min(m.selRow, len(rows)-1)
	for i := range m.columns {
		rowSel := -1
		if i == m.selCol {
			rowSel = selRow
		}
		table.profiles[i] = m.columns[i].pluginColumn(i, rows, rowSel)
	}
	return table.render()
}

// pluginColumn is this profile's table column: a three-line header
// (label, path, account or load status) plus one cell per matrix row.
// selRow marks the selected cell (-1 when the selection is elsewhere).
func (c *column) pluginColumn(idx int, rows []model.PluginRow, selRow int) tableColumn {
	labelCell := tableCell{text: c.profile.Label, style: labelStyle}
	if selRow >= 0 {
		labelCell.style = labelStyle.Underline(true)
	}
	col := tableColumn{
		header: []tableCell{
			labelCell,
			{text: c.profile.Path, style: pathStyle},
			c.statusCell(),
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

// statusCell is the third header line: the account while loaded, otherwise
// the column's load state (spinner or error).
func (c *column) statusCell() tableCell {
	switch c.status {
	case statusLoaded:
		var parts []string
		if c.auth.Email != "" {
			parts = append(parts, c.auth.Email)
		}
		if c.auth.SubscriptionType != "" {
			parts = append(parts, c.auth.SubscriptionType)
		}
		if len(parts) == 0 {
			return tableCell{text: "not logged in", style: pathStyle}
		}
		return tableCell{text: strings.Join(parts, " · ")}
	case statusError:
		return tableCell{text: "error: " + c.err.Error(), style: errStyle}
	default:
		return tableCell{text: c.spinner.View() + " loading…"}
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
func pinnedPluginColumn(rows []model.PluginRow) tableColumn {
	const title = "plugin@marketplace"
	idW := lipgloss.Width(title)
	for _, row := range rows {
		idW = max(idW, lipgloss.Width(row.ID.String()))
	}

	col := tableColumn{
		// Two blank lines align the title with the last profile-header line.
		header: []tableCell{{}, {}, {
			text:  fmt.Sprintf("%-*s  %s", idW, title, "latest"),
			style: labelStyle,
		}},
		cells: make([]tableCell, len(rows)),
	}
	for i, row := range rows {
		text := fmt.Sprintf("%-*s  %s", idW, row.ID.String(), versionText(row.LatestVersion))
		col.cells[i] = tableCell{text: strings.TrimRight(text, " ")}
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
