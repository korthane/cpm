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
	// IsDefault marks the profile that is $HOME/.claude — the dir the claude
	// CLI uses when CLAUDE_CONFIG_DIR is unset. On macOS its Keychain entries
	// live under a different service name than CLAUDE_CONFIG_DIR-set runs,
	// so the UI needs to know which column may require an env-stripped auth
	// fallback. Never read from YAML: derived during normalization.
	IsDefault bool `yaml:"-"`
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
// cliArgs win, else a non-empty config, else the discovered profiles. Paths
// have ~ expanded against homeDir and are de-duplicated by resolved path,
// preserving first-seen order; labels default to the path basename when
// unset. An empty path is an error.
func ResolveProfiles(cliArgs []string, cfg Config, discovered []Profile, homeDir string) ([]Profile, error) {
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
		// Discovered profiles need the dedup too: os.Stat in AutoDiscover
		// follows symlinks, so ~/.claude-work -> ~/.claude would otherwise
		// become two columns independently mutating one config dir.
		return normalize(discovered, homeDir)
	}
}

// normalize expands ~, fills default labels, and de-dups by resolved path while
// preserving order. Paths are cleaned and symlink-resolved before de-duping so
// variants like `~/.claude`, `~/.claude/`, or a symlink to it cannot become two
// columns independently mutating one config dir.
func normalize(profiles []Profile, homeDir string) ([]Profile, error) {
	defaultDir := filepath.Join(homeDir, ".claude")
	defaultPath := seenPath{key: dedupKey(defaultDir), info: statOrNil(defaultDir)}
	var seen []seenPath
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		if p.Path == "" {
			// filepath.Clean("") is "." — a blank CLI arg or a config entry
			// missing `path` would otherwise silently target the current
			// directory as a config dir.
			if p.Label != "" {
				return nil, fmt.Errorf("profile %q has an empty path", p.Label)
			}
			return nil, errors.New("empty profile path")
		}
		path := filepath.Clean(expandTilde(p.Path, homeDir))
		candidate := seenPath{key: dedupKey(path), info: statOrNil(path)}
		if isDuplicate(seen, candidate) {
			continue
		}
		seen = append(seen, candidate)
		out = append(out, Profile{
			Path:      path,
			Label:     cmp.Or(p.Label, filepath.Base(path)),
			IsDefault: isDuplicate([]seenPath{defaultPath}, candidate),
		})
	}
	return out, nil
}

// seenPath is a previously-accepted profile path, kept in both string and
// stat form so isDuplicate can catch aliases EvalSymlinks' string comparison
// misses, such as case-insensitive filesystems where "~/.claude" and
// "~/.Claude" are the same directory but resolve to different strings.
type seenPath struct {
	key  string
	info os.FileInfo
}

func statOrNil(path string) os.FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return info
}

// isDuplicate reports whether candidate refers to the same config dir as any
// already-seen path, either by resolved-path string or by device+inode.
func isDuplicate(seen []seenPath, candidate seenPath) bool {
	for _, s := range seen {
		if s.key == candidate.key {
			return true
		}
		if s.info != nil && candidate.info != nil && os.SameFile(s.info, candidate.info) {
			return true
		}
	}
	return false
}

// dedupKey resolves symlinks so a symlinked alias of an already-listed config
// dir cannot become a second column; a path that does not resolve (e.g. does
// not exist) falls back to its cleaned form.
func dedupKey(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
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
