// Command cpm is a terminal UI for comparing and managing Claude Code
// configuration (plugins, MCP servers) across multiple profiles.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/ui"
)

func main() {
	profiles, err := resolveProfiles(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "cpm:", err)
		os.Exit(1)
	}

	m := ui.New(claudecli.NewRunner(), profiles)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "cpm:", err)
		os.Exit(1)
	}
}

// resolveProfiles applies the discovery precedence (CLI args > config file >
// auto-discover) and fails when no profile can be found.
func resolveProfiles(cliArgs []string) ([]config.Profile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(home, ".config", "cpm", "config.yaml"))
	if err != nil {
		return nil, err
	}

	discovered, err := config.AutoDiscover(home)
	if err != nil {
		return nil, err
	}

	profiles := config.ResolveProfiles(cliArgs, cfg, discovered, home)
	if len(profiles) == 0 {
		return nil, errors.New("no profiles found: pass directories as arguments " +
			"or configure ~/.config/cpm/config.yaml")
	}
	return profiles, nil
}
