// Command cpm is a terminal UI for comparing and managing Claude Code
// configuration (plugins, MCP servers) across multiple profiles, with
// non-interactive commands (outdated, refresh) for scripts and agents.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/cli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/ui"
)

const usage = `usage: cpm [<profile-dir> ...]
       cpm outdated [--refresh] [--changelog] [--text|--json] [<profile-dir> ...]
       cpm refresh [--text|--json] [<profile-dir> ...]
       cpm -h | --help | <command> --help

With no command, cpm starts the terminal UI. Commands print a result and
exit:

  outdated   list installed plugins with a newer catalog version
             (--refresh runs 'claude plugin marketplace update' first;
             --changelog adds CHANGELOG.md entries since the oldest install)
  refresh    run 'claude plugin marketplace update' in every profile

  --text     human-readable output (default)
  --json     machine-readable output

Command exit codes: 0 success (outdated plugins found is still success);
1 a profile failed or could not be resolved, or output could not be
written; 2 a malformed command line after the command name.
` + cli.ProfilesNote

func main() {
	l := launcher{runner: claudecli.NewRunner(), startTUI: startTUI}
	os.Exit(l.run(os.Args[1:], os.Stdout, os.Stderr))
}

// launcher routes a command line to a CLI command or the TUI; its
// dependencies are fields so routing is testable without a TTY.
type launcher struct {
	runner   claudecli.Runner
	startTUI func(claudecli.Runner, []config.Profile) error
}

func startTUI(r claudecli.Runner, profiles []config.Profile) error {
	_, err := tea.NewProgram(ui.New(r, profiles), tea.WithAltScreen()).Run()
	return err
}

// run dispatches args and returns the process exit code.
func (l launcher) run(args []string, stdout, stderr io.Writer) int {
	// Commands go first so `cpm outdated --help` reaches the command usage.
	if len(args) > 0 && cli.IsCommand(args[0]) {
		return l.runCommand(args, stdout, stderr)
	}
	if slices.ContainsFunc(args, cli.IsHelpFlag) {
		_, err := fmt.Fprintln(stdout, usage)
		return cli.ExitCode(false, err, stderr)
	}

	profiles, err := resolveProfiles(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cpm:", err)
		return cli.ExitFailure
	}
	if err := l.startTUI(l.runner, profiles); err != nil {
		_, _ = fmt.Fprintln(stderr, "cpm:", err)
		return cli.ExitFailure
	}
	return cli.ExitOK
}

// runCommand runs a non-interactive command. Usage and profile-resolution
// errors precede cli.Run, so they are plain stderr text even under --json.
func (l launcher) runCommand(args []string, stdout, stderr io.Writer) int {
	opts, err := cli.ParseArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cpm:", err)
		return cli.ExitUsage
	}
	var profiles []config.Profile
	if !opts.Help {
		profiles, err = resolveProfiles(opts.Dirs)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "cpm:", err)
			return cli.ExitFailure
		}
	}
	// Cancelling on a signal lets the runner kill each claude process group.
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Run(ctx, l.runner, profiles, opts, stdout, stderr)
}

// resolveProfiles applies the discovery precedence (CLI args > config file >
// auto-discover) and fails when no profile can be found.
func resolveProfiles(cliArgs []string) ([]config.Profile, error) {
	// Profile dirs never start with "-"; a dashed argument is a flag typo.
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
