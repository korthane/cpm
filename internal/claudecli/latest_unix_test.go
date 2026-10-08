//go:build unix

package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Opening a FIFO blocks until a writer appears, so a FIFO committed to a
// marketplace repo must be skipped rather than read.
func TestLoadPluginsCachedSkipsFIFOCatalogFiles(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, `{"plugins": [{"name": "foo", "source": "./foo"}]}`)
	sub := filepath.Join(dir, "foo", ".claude-plugin")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(sub, "plugin.json"), 0o644); err != nil {
		t.Fatal(err)
	}

	stubGitCommitInfo(t, func(context.Context, string) (string, string, error) {
		return "", "", errors.New("not a git repository")
	})
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [{"id": "foo@m1", "version": "1.0.0", "enabled": true, "scope": "user"}],
				"available": []
			}`)},
			"plugin marketplace list --json": {Stdout: []byte(`[
				{"name": "m1", "installLocation": ` + strconv.Quote(dir) + `}
			]`)},
		},
	}

	type result struct {
		lv  LatestVersions
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, lv, err := LoadPluginsCached(t.Context(), f, "")
		done <- result{lv, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if v := r.lv.Versions[PluginID{Name: "foo", Marketplace: "m1"}]; v != "" {
			t.Errorf("foo@m1 = %q, want empty", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("load blocked on a FIFO plugin.json")
	}
}

func TestReadChangelogRefusesFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "CHANGELOG.md"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := ReadChangelog(PluginSource{Path: ".", CloneDir: dir})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error for a FIFO changelog")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadChangelog blocked on a FIFO")
	}
}
