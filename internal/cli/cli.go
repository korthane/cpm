// Package cli implements cpm's non-interactive commands (`cpm outdated`,
// `cpm refresh`): each prints a result for the given profiles and exits,
// without starting the TUI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

// Format selects how a command renders its result.
type Format string

// Output formats accepted by every command.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Options is a parsed command line for one command.
type Options struct {
	Command string
	Format  Format
	// Refresh runs `plugin marketplace update` before reading catalogs
	// (outdated only).
	Refresh bool
	// Help asks for the command's usage instead of running it.
	Help bool
	// Dirs are the profile dirs given on the command line, in order.
	Dirs []string
}

// Process exit codes, a documented contract for scripts.
const (
	ExitOK      = 0
	ExitFailure = 1 // a profile failed, or output could not be written
	ExitUsage   = 2 // malformed command line
)

// loadTimeout bounds each profile's work.
const loadTimeout = claudecli.CommandTimeout

type command struct {
	usage string
	// refreshFlag reports whether the command accepts --refresh.
	refreshFlag bool
	run         func(ctx context.Context, r claudecli.Runner,
		profiles []config.Profile, opts Options, stdout, stderr io.Writer) int
}

// ProfilesNote explains where profiles come from when none are given; it
// ends every usage text.
const ProfilesNote = `
With no <profile-dir>, profiles come from ~/.config/cpm/config.yaml or are
auto-discovered as ~/.claude* directories. Pass a profile dir named like a
command as ./outdated.`

var commands = map[string]command{
	"outdated": {
		usage: `usage: cpm outdated [--refresh] [--text|--json] [<profile-dir> ...]

List installed plugins with a newer version in a marketplace catalog, and
the profiles that have them installed.

  --refresh  run 'claude plugin marketplace update' first
  --text     human-readable output (default)
  --json     machine-readable output
` + ProfilesNote,
		refreshFlag: true,
		run:         runOutdated,
	},
	"refresh": {
		usage: `usage: cpm refresh [--text|--json] [<profile-dir> ...]

Run 'claude plugin marketplace update' in every profile and report the
result for each one.

  --text     human-readable output (default)
  --json     machine-readable output
` + ProfilesNote,
		run: runRefresh,
	},
}

// IsCommand reports whether name is a cpm command rather than a profile dir.
func IsCommand(name string) bool {
	_, ok := commands[name]
	return ok
}

// ParseArgs parses `<command> [flags] [<profile-dir> ...]`; flags and dirs
// may be interleaved. Every error is a usage error, prefixed by the command
// name once the command is known. A help flag anywhere
// wins over other flag errors, so `--help` always reaches the usage text.
func ParseArgs(args []string) (Options, error) {
	if len(args) == 0 {
		return Options{}, errors.New("missing command")
	}
	name := args[0]
	cmd, ok := commands[name]
	if !ok {
		return Options{}, fmt.Errorf("unknown command %q", name)
	}
	opts := Options{Command: name, Format: FormatText}
	rest := args[1:]
	if slices.ContainsFunc(rest, IsHelpFlag) {
		opts.Help = true
		return opts, nil
	}

	var text, json bool
	for _, arg := range rest {
		switch {
		case arg == "--text":
			text = true
		case arg == "--json":
			json = true
		case arg == "--refresh" && cmd.refreshFlag:
			opts.Refresh = true
		case strings.HasPrefix(arg, "-"):
			// Dashed dirs are rejected too: they would read as a flag typo.
			return Options{}, fmt.Errorf("%s: unknown flag %q", name, arg)
		default:
			opts.Dirs = append(opts.Dirs, arg)
		}
	}
	if text && json {
		return Options{}, fmt.Errorf(
			"%s: --text and --json are mutually exclusive", name)
	}
	if json {
		opts.Format = FormatJSON
	}
	return opts, nil
}

// IsHelpFlag reports whether arg asks for usage text.
func IsHelpFlag(arg string) bool {
	return arg == "-h" || arg == "--help"
}

// Run executes opts.Command against profiles and returns the process exit
// code: 0 success, 1 a profile failed or stdout could not be written,
// 2 usage error. In JSON mode command results and per-profile errors are
// carried in the JSON and stderr stays empty; usage errors (including an
// unknown command) and a failed stdout write stay plain stderr text.
func Run(ctx context.Context, r claudecli.Runner, profiles []config.Profile,
	opts Options, stdout, stderr io.Writer) int {
	cmd, ok := commands[opts.Command]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "cpm: unknown command %q\n", opts.Command)
		return ExitUsage
	}
	if opts.Help {
		_, err := fmt.Fprintln(stdout, cmd.usage)
		return ExitCode(false, err, stderr)
	}
	return cmd.run(ctx, r, profiles, opts, stdout, stderr)
}

// mapProfiles runs fn for every profile in parallel, each under loadTimeout;
// results keep profile order. Each goroutine owns its own config dir, so
// writes never overlap within this process.
func mapProfiles[T any](ctx context.Context, profiles []config.Profile,
	fn func(context.Context, config.Profile) T) []T {
	results := make([]T, len(profiles))
	var wg sync.WaitGroup
	for i, p := range profiles {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, loadTimeout)
			defer cancel()
			results[i] = fn(ctx, p)
		})
	}
	wg.Wait()
	return results
}

// profileLabels maps each profile path to its display label: the configured
// label, or the path when the label is empty or shared with another profile
// (default labels are directory names, so /a/.claude and /b/.claude clash).
func profileLabels(profiles []config.Profile) map[string]string {
	uses := map[string]int{}
	for _, p := range profiles {
		uses[p.Label]++
	}
	labels := make(map[string]string, len(profiles))
	for _, p := range profiles {
		labels[p.Path] = p.Label
		if p.Label == "" || uses[p.Label] > 1 {
			labels[p.Path] = p.Path
		}
	}
	return labels
}

// quoteControl Go-quotes text holding control characters, so third-party
// values (catalog versions, CLI errors) cannot forge text output lines or
// inject terminal escapes.
func quoteControl(s string) string {
	if strings.ContainsFunc(s, unicode.IsControl) {
		return strconv.Quote(s)
	}
	return s
}

// ExitCode maps a command's outcome to its exit code. A failed stdout write
// exits 1 even when every profile succeeded: the result is incomplete.
func ExitCode(profileFailed bool, writeErr error, stderr io.Writer) int {
	if writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "cpm: write output: %v\n", writeErr)
		return ExitFailure
	}
	if profileFailed {
		return ExitFailure
	}
	return ExitOK
}
