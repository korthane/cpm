package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Run("missing file yields empty config", func(t *testing.T) {
		cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.Profiles) != 0 {
			t.Fatalf("expected no profiles, got %v", cfg.Profiles)
		}
	})

	t.Run("parses profiles with and without labels", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		data := "profiles:\n  - path: ~/.claude\n    label: personal\n  - path: ~/.claude-work\n"
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{
			{Path: "~/.claude", Label: "personal"},
			{Path: "~/.claude-work"},
		}
		if !reflect.DeepEqual(cfg.Profiles, want) {
			t.Fatalf("got %+v, want %+v", cfg.Profiles, want)
		}
	})

	t.Run("malformed yaml is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(path, []byte("profiles: [oops\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("expected error for malformed yaml")
		}
	})

	t.Run("unreadable file is an error, not an empty config", func(t *testing.T) {
		// A directory triggers a non-ENOENT read error.
		if _, err := LoadConfig(t.TempDir()); err == nil {
			t.Fatal("expected error for unreadable config path")
		}
	})

	t.Run("unknown key is an error, not an empty config", func(t *testing.T) {
		// A typo like `profile:` must not silently fall back to
		// auto-discovery.
		path := filepath.Join(t.TempDir(), "typo.yaml")
		if err := os.WriteFile(path, []byte("profile:\n  - path: ~/.claude\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("expected error for unknown config key")
		}
	})

	t.Run("empty file yields empty config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.yaml")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.Profiles) != 0 {
			t.Fatalf("expected no profiles, got %v", cfg.Profiles)
		}
	})
}

func TestAutoDiscover(t *testing.T) {
	home := t.TempDir()
	// Config dirs that should be discovered.
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(home, ".claude-work"))
	mustMkdir(t, filepath.Join(home, ".claude-personal"))
	// A non-matching dir and a matching-name file should be ignored.
	mustMkdir(t, filepath.Join(home, ".config"))
	if err := os.WriteFile(filepath.Join(home, ".claudeignore"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := AutoDiscover(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Profile{
		{Path: filepath.Join(home, ".claude"), Label: ".claude"},
		{Path: filepath.Join(home, ".claude-personal"), Label: ".claude-personal"},
		{Path: filepath.Join(home, ".claude-work"), Label: ".claude-work"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveProfiles(t *testing.T) {
	home := "/home/tester"
	cfg := Config{Profiles: []Profile{
		{Path: "~/.claude", Label: "cfg-personal"},
		{Path: "~/.claude-work"},
	}}
	discovered := []Profile{
		{Path: "/home/tester/.claude", Label: ".claude"},
		{Path: "/home/tester/.claude-old", Label: ".claude-old"},
	}

	t.Run("cli args win and restrict to exactly the given set", func(t *testing.T) {
		got, err := ResolveProfiles([]string{"~/.claude", "/abs/other"}, cfg, discovered, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{
			{Path: "/home/tester/.claude", Label: ".claude", IsDefault: true},
			{Path: "/abs/other", Label: "other"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("config wins over discovery when no cli args", func(t *testing.T) {
		got, err := ResolveProfiles(nil, cfg, discovered, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{
			{Path: "/home/tester/.claude", Label: "cfg-personal", IsDefault: true},
			{Path: "/home/tester/.claude-work", Label: ".claude-work"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("auto-discovery when no cli args and empty config", func(t *testing.T) {
		got, err := ResolveProfiles(nil, Config{}, discovered, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{
			{Path: "/home/tester/.claude", Label: ".claude", IsDefault: true},
			{Path: "/home/tester/.claude-old", Label: ".claude-old"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("dedup preserves first occurrence and order", func(t *testing.T) {
		got, err := ResolveProfiles([]string{"~/.claude", "/home/tester/.claude", "~/.other"}, cfg, discovered, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{
			{Path: "/home/tester/.claude", Label: ".claude", IsDefault: true},
			{Path: "/home/tester/.other", Label: ".other"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("dedup collapses path variants of one directory", func(t *testing.T) {
		// Two columns for one config dir would mutate it concurrently.
		got, err := ResolveProfiles(
			[]string{"~/.claude", "/home/tester/.claude/", "/home/tester/.claude-x/../.claude"},
			cfg, discovered, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{{Path: "/home/tester/.claude", Label: ".claude", IsDefault: true}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("dedup collapses a symlink to an already-listed dir", func(t *testing.T) {
		dir := t.TempDir()
		realDir := filepath.Join(dir, "claude")
		mustMkdir(t, realDir)
		link := filepath.Join(dir, "claude-link")
		if err := os.Symlink(realDir, link); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveProfiles([]string{realDir, link}, Config{}, nil, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{{Path: realDir, Label: "claude"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("dedup collapses hard-linked paths EvalSymlinks does not normalize", func(t *testing.T) {
		// A hard link produces two distinct, non-symlink paths to the same
		// inode, so EvalSymlinks returns each path unchanged (they differ as
		// strings). This is the same shape of gap as a case-insensitive
		// filesystem resolving "~/.claude" and "~/.Claude" to one directory
		// while EvalSymlinks preserves the input casing in each string.
		dir := t.TempDir()
		original := filepath.Join(dir, "claude")
		if err := os.WriteFile(original, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		linked := filepath.Join(dir, "claude-alias")
		if err := os.Link(original, linked); err != nil {
			t.Skipf("hard links unsupported on this filesystem: %v", err)
		}
		got, err := ResolveProfiles([]string{original, linked}, Config{}, nil, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{{Path: original, Label: "claude"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("discovered symlink alias collapses to one profile", func(t *testing.T) {
		// AutoDiscover stats through symlinks, so ~/.claude-work -> ~/.claude
		// yields two entries for one physical config dir; resolution must
		// collapse them or two columns would mutate the same dir concurrently.
		tmpHome := t.TempDir()
		realDir := filepath.Join(tmpHome, ".claude")
		mustMkdir(t, realDir)
		if err := os.Symlink(realDir, filepath.Join(tmpHome, ".claude-work")); err != nil {
			t.Fatal(err)
		}
		discovered, err := AutoDiscover(tmpHome)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(discovered) != 2 {
			t.Fatalf("discovered %+v, want the dir and its symlink alias", discovered)
		}
		got, err := ResolveProfiles(nil, Config{}, discovered, tmpHome)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{{Path: realDir, Label: ".claude", IsDefault: true}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("bare tilde expands to home", func(t *testing.T) {
		got, err := ResolveProfiles([]string{"~"}, Config{}, nil, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{{Path: "/home/tester", Label: "tester"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("empty cli arg is an error, not the current directory", func(t *testing.T) {
		// filepath.Clean("") is "." — an empty path must not silently make
		// cpm mutate a config dir rooted at the cwd.
		if _, err := ResolveProfiles([]string{""}, Config{}, nil, home); err == nil {
			t.Fatal("expected error for an empty cli path")
		}
	})

	t.Run("config entry without a path is an error naming its label", func(t *testing.T) {
		cfg := Config{Profiles: []Profile{{Label: "work"}}}
		_, err := ResolveProfiles(nil, cfg, nil, home)
		if err == nil {
			t.Fatal("expected error for a config entry without a path")
		}
		if !strings.Contains(err.Error(), "work") {
			t.Fatalf("err = %v, want it to name the entry's label", err)
		}
	})
}

func TestResolveProfilesIsDefault(t *testing.T) {
	home := "/home/tester"

	t.Run("direct path to ~/.claude is default, others are not", func(t *testing.T) {
		got, err := ResolveProfiles([]string{"~/.claude", "~/.claude-work"}, Config{}, nil, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []Profile{
			{Path: "/home/tester/.claude", Label: ".claude", IsDefault: true},
			{Path: "/home/tester/.claude-work", Label: ".claude-work"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("trailing slash still detects the default", func(t *testing.T) {
		got, err := ResolveProfiles([]string{"/home/tester/.claude/"}, Config{}, nil, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || !got[0].IsDefault {
			t.Fatalf("got %+v, want a single default profile", got)
		}
	})

	t.Run("symlink to ~/.claude is default", func(t *testing.T) {
		tmpHome := t.TempDir()
		realDir := filepath.Join(tmpHome, ".claude")
		mustMkdir(t, realDir)
		link := filepath.Join(tmpHome, "claude-alias")
		if err := os.Symlink(realDir, link); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveProfiles([]string{link}, Config{}, nil, tmpHome)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || !got[0].IsDefault {
			t.Fatalf("got %+v, want a single default profile", got)
		}
	})

	t.Run("sibling dir named like the default is not default", func(t *testing.T) {
		got, err := ResolveProfiles([]string{"~/.claude-work", "~/other/.claude"}, Config{}, nil, home)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, p := range got {
			if p.IsDefault {
				t.Fatalf("profile %+v must not be default", p)
			}
		}
	})

	t.Run("empty home skips default detection", func(t *testing.T) {
		// With an unresolvable home the comparison target would be the
		// relative ".claude" — a cwd-local dir passed as an arg must not be
		// mistaken for the default profile.
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		got, err := ResolveProfiles([]string{filepath.Join(cwd, ".claude")}, Config{}, nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].IsDefault {
			t.Fatalf("got %+v, want a single non-default profile", got)
		}
	})
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
