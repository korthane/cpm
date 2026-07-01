package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProfilesFromArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p1 := t.TempDir()
	p2 := t.TempDir()

	profiles, err := resolveProfiles([]string{p1, p2})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Path != p1 || profiles[1].Path != p2 {
		t.Fatalf("profiles = %+v, want %s and %s", profiles, p1, p2)
	}
}

func TestResolveProfilesAutoDiscovers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	profiles, err := resolveProfiles(nil)
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Path != dir {
		t.Fatalf("profiles = %+v, want just %s", profiles, dir)
	}
}

func TestResolveProfilesErrorsWhenNoneFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := resolveProfiles(nil); err == nil {
		t.Fatal("resolveProfiles with empty home returned no error")
	}
}

func TestResolveProfilesRejectsFlagLikeArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := resolveProfiles([]string{"--bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want unknown flag error", err)
	}
}

func TestResolveProfilesRejectsMissingDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := resolveProfiles([]string{"/nonexistent/profile-dir"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v, want not-a-directory error", err)
	}
}

func TestResolveProfilesRejectsMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "cpm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profiles: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	// A broken config must abort, not silently fall back to auto-discovery.
	if _, err := resolveProfiles(nil); err == nil {
		t.Fatal("resolveProfiles with malformed config returned no error")
	}
}

func TestResolveProfilesArgsIgnoreMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "cpm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profiles: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	// CLI args win the precedence, so the (ignored) broken config must not
	// block an explicit invocation.
	p := t.TempDir()
	profiles, err := resolveProfiles([]string{p})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Path != p {
		t.Fatalf("profiles = %+v, want just %s", profiles, p)
	}
}
