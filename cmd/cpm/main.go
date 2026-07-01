// Command cpm is a terminal UI for comparing and managing Claude Code
// configuration (plugins, MCP servers) across multiple profiles.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/ui"
)

const usage = `usage: cpm [<profile-dir> ...]

With no arguments, profiles come from ~/.config/cpm/config.yaml or are
auto-discovered as ~/.claude* directories.`

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			fmt.Println(usage)
			return
		}
	}

	profiles, err := resolveProfiles(args)
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
	// cpm takes no flags; a dashed argument is a typo, not a profile dir.
	for _, arg := range cliArgs {
		if strings.HasPrefix(arg, "-") {
			return nil, fmt.Errorf("unknown flag %q", arg)
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	// CLI args take full precedence, so a broken config file or a discovery
	// failure must not block an explicit `cpm <dir>` invocation that would
	// ignore both anyway.
	var cfg config.Config
	var discovered []config.Profile
	if len(cliArgs) == 0 {
		cfg, err = config.LoadConfig(filepath.Join(home, ".config", "cpm", "config.yaml"))
		if err != nil {
			return nil, err
		}
		discovered, err = config.AutoDiscover(home)
		if err != nil {
			return nil, err
		}
	}

	profiles := config.ResolveProfiles(cliArgs, cfg, discovered, home)
	if len(profiles) == 0 {
		return nil, errors.New("no profiles found: pass directories as arguments " +
			"or configure ~/.config/cpm/config.yaml")
	}
	// Fail fast on typos: a missing directory would otherwise surface as a
	// confusing per-column CLI error inside the TUI.
	for _, p := range profiles {
		info, err := os.Stat(p.Path)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("profile %s is not a directory", p.Path)
		}
	}
	return profiles, nil
}
