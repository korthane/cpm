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

	// $HOME resolution is best-effort: normalize needs it to mark the
	// ~/.claude profile IsDefault (the Keychain auth fallback) even when all
	// args are absolute paths. It is only *required* for config/auto-discover
	// lookup and for expanding a leading "~" in an explicit arg, so an
	// unresolvable $HOME (e.g. a minimal container) must not block an
	// absolute-path invocation.
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		home = ""
		if len(cliArgs) == 0 || needsHome(cliArgs) {
			return nil, fmt.Errorf("resolve home dir: %w", homeErr)
		}
	}
	var cfg config.Config
	var discovered []config.Profile
	if len(cliArgs) == 0 {
		var err error
		cfg, err = config.LoadConfig(filepath.Join(home, ".config", "cpm", "config.yaml"))
		if err != nil {
			return nil, err
		}
		// Skip auto-discover once config profiles exist: ResolveProfiles would
		// ignore discovered profiles anyway, and a valid config shouldn't fail
		// due to an unrelated auto-discover error (e.g. a $HOME containing
		// glob metacharacters that make filepath.Glob return ErrBadPattern).
		if len(cfg.Profiles) == 0 {
			discovered, err = config.AutoDiscover(home)
			if err != nil {
				return nil, err
			}
		}
	}

	profiles, err := config.ResolveProfiles(cliArgs, cfg, discovered, home)
	if err != nil {
		return nil, err
	}
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

// needsHome reports whether any arg requires $HOME to expand a leading "~".
func needsHome(cliArgs []string) bool {
	for _, arg := range cliArgs {
		if arg == "~" || strings.HasPrefix(arg, "~/") {
			return true
		}
	}
	return false
}
