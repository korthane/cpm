// Command cpm is a terminal UI for comparing and managing Claude Code
// configuration (plugins, MCP servers) across multiple profiles.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "cpm:", err)
		os.Exit(1)
	}
}

// model is the root Bubble Tea model. For now it renders a placeholder and
// quits on q / ctrl+c; the real table UI is built in later tasks.
type model struct{}

func newModel() model { return model{} }

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	return "CPM — Claude Plugin Manager\n\npress q to quit\n"
}
