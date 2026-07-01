package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProfilesFromArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	profiles, err := resolveProfiles([]string{"/tmp/p1", "/tmp/p2"})
	if err != nil {
		t.Fatalf("resolveProfiles: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Path != "/tmp/p1" || profiles[1].Path != "/tmp/p2" {
		t.Fatalf("profiles = %+v, want /tmp/p1 and /tmp/p2", profiles)
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
