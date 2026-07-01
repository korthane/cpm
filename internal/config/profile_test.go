package config

import (
	"os"
	"path/filepath"
	"reflect"
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
		got := ResolveProfiles([]string{"~/.claude", "/abs/other"}, cfg, discovered, home)
		want := []Profile{
			{Path: "/home/tester/.claude", Label: ".claude"},
			{Path: "/abs/other", Label: "other"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("config wins over discovery when no cli args", func(t *testing.T) {
		got := ResolveProfiles(nil, cfg, discovered, home)
		want := []Profile{
			{Path: "/home/tester/.claude", Label: "cfg-personal"},
			{Path: "/home/tester/.claude-work", Label: ".claude-work"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("auto-discovery when no cli args and empty config", func(t *testing.T) {
		got := ResolveProfiles(nil, Config{}, discovered, home)
		if !reflect.DeepEqual(got, discovered) {
			t.Fatalf("got %+v, want %+v", got, discovered)
		}
	})

	t.Run("dedup preserves first occurrence and order", func(t *testing.T) {
		got := ResolveProfiles([]string{"~/.claude", "/home/tester/.claude", "~/.other"}, cfg, discovered, home)
		want := []Profile{
			{Path: "/home/tester/.claude", Label: ".claude"},
			{Path: "/home/tester/.other", Label: ".other"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("bare tilde expands to home", func(t *testing.T) {
		got := ResolveProfiles([]string{"~"}, Config{}, nil, home)
		want := []Profile{{Path: "/home/tester", Label: "tester"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
