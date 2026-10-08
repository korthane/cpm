package claudecli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/model"
)

// Regression: `plugin list --available` leaves installed plugins out, so
// their latest version must come from the marketplace's catalog file.
func TestLoadPluginsCachedInstalledPluginResolvesFromCatalogFile(t *testing.T) {
	t.Cleanup(claudecli.StubGitCommitInfo(
		func(context.Context, string) (string, string, error) {
			return "", "", errors.New("not a git repository")
		}))
	dir := t.TempDir()
	sub := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := `{"plugins": [{"name": "foo", "version": "0.35.1"}]}`
	if err := os.WriteFile(filepath.Join(sub, "marketplace.json"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [{"id": "foo@acme", "version": "0.34.0", "enabled": true, "scope": "user"}],
				"available": []
			}`)},
			"plugin marketplace list --json": {Stdout: []byte(`[
				{"name": "acme", "installLocation": ` + strconv.Quote(dir) + `}
			]`)},
		},
	}

	data, lv, err := claudecli.LoadPluginsCached(t.Context(), f, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foo := claudecli.PluginID{Name: "foo", Marketplace: "acme"}
	if v := lv.Versions[foo]; v != "0.35.1" {
		t.Fatalf("foo@acme = %q, want %q", v, "0.35.1")
	}

	latest, _ := model.MergeLatestVersions([]claudecli.LatestVersions{lv})
	rows := model.BuildPluginMatrix([]claudecli.PluginData{data}, latest)
	if len(rows) != 1 || rows[0].ID != foo {
		t.Fatalf("rows = %+v, want one foo@acme row", rows)
	}
	if !rows[0].Cells[0].Outdated {
		t.Errorf("cell = %+v, want Outdated", rows[0].Cells[0])
	}
}
