package cli

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

func TestIsCommand(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"outdated": true,
		"refresh":  true,
		"update":   false,
		"":         false,
		"--help":   false,
	} {
		if got := IsCommand(name); got != want {
			t.Errorf("IsCommand(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParseArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{
			name: "command only defaults to text",
			args: []string{"refresh"},
			want: Options{Command: "refresh", Format: FormatText},
		},
		{
			name: "interleaved flags and dirs",
			args: []string{"outdated", "/a", "--refresh", "/b", "--json", "/c"},
			want: Options{
				Command: "outdated", Format: FormatJSON, Refresh: true,
				Dirs: []string{"/a", "/b", "/c"},
			},
		},
		{
			name: "explicit text",
			args: []string{"refresh", "--text", "/a"},
			want: Options{
				Command: "refresh", Format: FormatText, Dirs: []string{"/a"},
			},
		},
		{
			name: "short help",
			args: []string{"outdated", "-h"},
			want: Options{Command: "outdated", Format: FormatText, Help: true},
		},
		{
			name: "help wins over a bad flag",
			args: []string{"refresh", "--bogus", "--help"},
			want: Options{Command: "refresh", Format: FormatText, Help: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseArgs(tt.args)
			if err != nil {
				t.Fatalf("ParseArgs(%q) error: %v", tt.args, err)
			}
			if got.Command != tt.want.Command || got.Format != tt.want.Format ||
				got.Refresh != tt.want.Refresh || got.Help != tt.want.Help ||
				!slices.Equal(got.Dirs, tt.want.Dirs) {
				t.Errorf("ParseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseArgsUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"no command", nil, "missing command"},
		{"unknown command", []string{"update"}, `unknown command "update"`},
		{"text and json", []string{"outdated", "--text", "--json"},
			"--text and --json are mutually exclusive"},
		{"unknown flag", []string{"outdated", "--bogus"},
			`unknown flag "--bogus"`},
		{"dashed dir is an unknown flag", []string{"refresh", "-dir"},
			`unknown flag "-dir"`},
		{"refresh flag only for outdated", []string{"refresh", "--refresh"},
			`unknown flag "--refresh"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseArgs(tt.args)
			var uerr *UsageError
			if !errors.As(err, &uerr) {
				t.Fatalf("ParseArgs(%q) error = %v, want *UsageError", tt.args, err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestUsageErrorNamesCommand(t *testing.T) {
	t.Parallel()
	_, err := ParseArgs([]string{"outdated", "--bogus"})
	var uerr *UsageError
	if !errors.As(err, &uerr) {
		t.Fatalf("error = %v, want *UsageError", err)
	}
	if uerr.Command != "outdated" {
		t.Errorf("Command = %q, want outdated", uerr.Command)
	}
	if !strings.HasPrefix(err.Error(), "outdated: ") {
		t.Errorf("error %q should be prefixed by the command", err)
	}
}

func TestRunHelpPrintsCommandUsage(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"outdated", "refresh"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			r := &claudecli.FakeRunner{}
			opts := Options{Command: cmd, Format: FormatJSON, Help: true}
			code := Run(context.Background(), r, nil, opts, &stdout, &stderr)
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if !strings.HasPrefix(stdout.String(), "usage: cpm "+cmd) {
				t.Errorf("stdout = %q, want %s usage", stdout.String(), cmd)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			if len(r.Calls) != 0 {
				t.Errorf("help ran claude: %v", r.Calls)
			}
		})
	}
}

func TestRunOutdatedUsageMentionsRefreshFlag(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	opts := Options{Command: "outdated", Help: true}
	Run(context.Background(), &claudecli.FakeRunner{}, nil, opts,
		&stdout, &bytes.Buffer{})
	for _, want := range []string{"--refresh", "--json", "--text", "./outdated"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("outdated usage lacks %q:\n%s", want, stdout.String())
		}
	}
}

// Usage errors stay plain stderr text even in JSON mode.
func TestRunUnknownCommandIsUsageError(t *testing.T) {
	t.Parallel()
	for _, format := range []Format{FormatText, FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			opts := Options{Command: "update", Format: format}
			code := Run(context.Background(), &claudecli.FakeRunner{}, nil, opts,
				&stdout, &stderr)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if want := "cpm: unknown command \"update\"\n"; stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}

// A lost result must not read as success, even when every profile loaded.
func TestRunFailedStdoutWriteExitsOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		runner func() *claudecli.FakeRunner
		opts   Options
	}{
		{"outdated text", mixedRunner,
			Options{Command: "outdated", Format: FormatText}},
		{"outdated text up to date", upToDateRunner,
			Options{Command: "outdated", Format: FormatText}},
		{"outdated json", mixedRunner,
			Options{Command: "outdated", Format: FormatJSON}},
		{"refresh text", func() *claudecli.FakeRunner { return refreshRunner() },
			Options{Command: "refresh", Format: FormatText}},
		{"refresh json", func() *claudecli.FakeRunner { return refreshRunner() },
			Options{Command: "refresh", Format: FormatJSON}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := Run(context.Background(), tt.runner(),
				[]config.Profile{homeProfile}, tt.opts,
				failingWriter{}, &stderr)
			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if want := "cpm: write output: disk full\n"; stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}
