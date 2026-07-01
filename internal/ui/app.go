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
)

// View renders the tab bar and a status line per profile column. Task 9
// replaces the per-profile lines with the full comparison table.
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
		for i := range m.columns {
			b.WriteString(m.columns[i].statusLine())
			b.WriteByte('\n')
		}
	case tabMCP:
		b.WriteString("MCP servers — coming in a later task\n")
	}

	b.WriteString("\ntab: switch  r: reload  q: quit\n")
	return b.String()
}

func (c *column) statusLine() string {
	head := c.profile.Label + " (" + c.profile.Path + ")"
	switch c.status {
	case statusLoaded:
		account := c.auth.Email
		if c.auth.SubscriptionType != "" {
			account += " · " + c.auth.SubscriptionType
		}
		if account == "" {
			account = "not logged in"
		}
		return fmt.Sprintf("%s  %s — %d plugins", head, account, len(c.plugins.Installed))
	case statusError:
		return head + "  " + errStyle.Render("error: "+c.err.Error())
	default:
		return head + "  " + c.spinner.View() + " loading…"
	}
}
