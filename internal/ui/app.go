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
	// scroll is the index of the leftmost visible profile column.
	scroll int
	width  int
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
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "left":
		m.scroll = max(0, m.scroll-1)
		return m, nil
	case "right":
		m.scroll = min(len(m.columns)-1, m.scroll+1)
		return m, nil
	case "r":
		for i := range m.columns {
			m.columns[i].status = statusLoading
			m.columns[i].err = nil
		}
		return m, m.loadAll()
	}
	return m, nil
}

var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Underline(true)
	inactiveTabStyle = lipgloss.NewStyle().Faint(true)
	errStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	labelStyle       = lipgloss.NewStyle().Bold(true)
	pathStyle        = lipgloss.NewStyle().Faint(true)
	absentStyle      = lipgloss.NewStyle().Faint(true)
	outdatedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
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

	b.WriteString("\ntab: switch  ←/→: scroll  r: reload  q: quit\n")
	return b.String()
}

func (m Model) viewPlugins() string {
	perProfile := make([]claudecli.PluginData, len(m.columns))
	for i := range m.columns {
		perProfile[i] = m.columns[i].plugins
	}
	rows := model.BuildPluginMatrix(perProfile, model.LatestVersions(perProfile))

	table := comparisonTable{
		profiles: make([]tableColumn, len(m.columns)),
		pinned:   pinnedPluginColumn(rows),
		scroll:   m.scroll,
		width:    m.width,
	}
	for i := range m.columns {
		table.profiles[i] = m.columns[i].pluginColumn(i, rows)
	}
	return table.render()
}

// pluginColumn is this profile's table column: a three-line header
// (label, path, account or load status) plus one cell per matrix row.
func (c *column) pluginColumn(idx int, rows []model.PluginRow) tableColumn {
	col := tableColumn{
		header: []tableCell{
			{text: c.profile.Label, style: labelStyle},
			{text: c.profile.Path, style: pathStyle},
			c.statusCell(),
		},
		cells: make([]tableCell, len(rows)),
	}
	for i, row := range rows {
		col.cells[i] = c.bodyCell(row.Cells[idx])
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
