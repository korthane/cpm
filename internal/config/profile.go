// Package config resolves the set of Claude Code profiles CPM operates on,
// applying the precedence CLI args > config file > auto-discovery.
package config

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile identifies a single Claude Code configuration directory
// (a CLAUDE_CONFIG_DIR) together with a human-facing label.
type Profile struct {
	Path  string `yaml:"path"`
	Label string `yaml:"label,omitempty"`
}

// Config is the optional ~/.config/cpm/config.yaml document.
type Config struct {
	Profiles []Profile `yaml:"profiles"`
}

// LoadConfig reads and parses the config file at path. A missing file is not an
// error: it yields an empty Config. Malformed YAML is reported as an error.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	// Strict decoding: an unknown key (e.g. `profile:` instead of `profiles:`)
	// would otherwise parse to an empty Config and silently fall back to
	// auto-discovery, hiding the typo.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) { // empty file
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// AutoDiscover finds home-level .claude* directories (e.g. ~/.claude,
// ~/.claude-work). Only directories are returned, sorted by name, so cache
// files and unrelated entries are ignored.
func AutoDiscover(homeDir string) ([]Profile, error) {
	matches, err := filepath.Glob(filepath.Join(homeDir, ".claude*"))
	if err != nil {
		return nil, fmt.Errorf("glob profiles: %w", err)
	}
	slices.Sort(matches)

	var profiles []Profile
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		profiles = append(profiles, Profile{Path: path, Label: filepath.Base(path)})
	}
	return profiles, nil
}

// ResolveProfiles selects the effective profile set by precedence: non-empty
// cliArgs win, else a non-empty config, else the discovered profiles. Paths from
// cliArgs and config have ~ expanded against homeDir and are de-duplicated by
// resolved path, preserving first-seen order; labels default to the path
// basename when unset.
func ResolveProfiles(cliArgs []string, cfg Config, discovered []Profile, homeDir string) []Profile {
	switch {
	case len(cliArgs) > 0:
		profiles := make([]Profile, len(cliArgs))
		for i, arg := range cliArgs {
			profiles[i] = Profile{Path: arg}
		}
		return normalize(profiles, homeDir)
	case len(cfg.Profiles) > 0:
		return normalize(cfg.Profiles, homeDir)
	default:
		return discovered
	}
}

// normalize expands ~, fills default labels, and de-dups by resolved path while
// preserving order. Paths are cleaned before de-duping so variants like
// `~/.claude` and `~/.claude/` cannot become two columns independently
// mutating one config dir.
func normalize(profiles []Profile, homeDir string) []Profile {
	seen := make(map[string]struct{}, len(profiles))
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		path := filepath.Clean(expandTilde(p.Path, homeDir))
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, Profile{Path: path, Label: cmp.Or(p.Label, filepath.Base(path))})
	}
	return out
}

func expandTilde(path, homeDir string) string {
	if path == "~" {
		return homeDir
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(homeDir, rest)
	}
	return path
}
