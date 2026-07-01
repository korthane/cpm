package claudecli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestListMarketplacesFixture(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace list --json": {Stdout: readFixture(t, "marketplace_list.json")},
		},
	}

	got, err := ListMarketplaces(t.Context(), f, "/home/u/.claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []Marketplace{
		{Name: "claude-plugins-official", InstallLocation: "/Users/u/.claude/plugins/marketplaces/claude-plugins-official"},
		{Name: "elastic-agent-skills", InstallLocation: "/Users/u/.claude/plugins/marketplaces/elastic-agent-skills"},
		{Name: "olomix-cc-thingz", InstallLocation: "/Users/u/src/github.com/olomix/cc-thingz"},
		{Name: "ralphex", InstallLocation: "/Users/u/.claude/plugins/marketplaces/ralphex"},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListMarketplacesErrors(t *testing.T) {
	tests := []struct {
		name   string
		stdout []byte
		runErr error
	}{
		{name: "malformed JSON", stdout: []byte(`[{`)},
		{name: "runner failure", runErr: errors.New("spawn failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FakeRunner{Default: FakeResponse{Stdout: tt.stdout, Err: tt.runErr}}
			if _, err := ListMarketplaces(t.Context(), f, ""); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// writeCatalog places a marketplace.json under dir/.claude-plugin/ the way a
// real marketplace install location lays it out.
func writeCatalog(t *testing.T, dir, content string) {
	t.Helper()
	sub := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "marketplace.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPluginsFreshReturnsDataAndVersions(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace update": {},
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [{"id": "a@m1", "version": "1.0.0", "enabled": true}],
				"available": [
					{"pluginId": "a@m1", "version": "1.2.0", "source": "./a"},
					{"pluginId": "b@m1", "source": {"source": "git-subdir", "ref": "v1.5.5"}}
				]
			}`)},
		},
	}

	data, lv, err := LoadPluginsFresh(t.Context(), f, "/home/u/.claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lv.Stale {
		t.Error("Stale = true, want false")
	}
	if len(data.Installed) != 1 || data.Installed[0].Version != "1.0.0" {
		t.Errorf("Installed = %+v, want one v1.0.0 plugin", data.Installed)
	}
	if v := lv.Versions[PluginID{Name: "a", Marketplace: "m1"}]; v != "1.2.0" {
		t.Errorf("a@m1 = %q, want %q", v, "1.2.0")
	}
	if v := lv.Versions[PluginID{Name: "b", Marketplace: "m1"}]; v != "v1.5.5" {
		t.Errorf("b@m1 = %q, want %q", v, "v1.5.5")
	}

	// One refresh, then one catalog read — the data and the versions must come
	// from the same single `plugin list` spawn, run after the refresh so the
	// versions are fresh.
	if len(f.Calls) != 2 {
		t.Fatalf("recorded %d calls, want 2: %v", len(f.Calls), f.Calls)
	}
	if strings.Join(f.Calls[0].Args, " ") != "plugin marketplace update" {
		t.Errorf("first call = %v, want marketplace update", f.Calls[0].Args)
	}
	if f.Calls[0].ProfileDir != "/home/u/.claude" {
		t.Errorf("refresh profile dir = %q, want /home/u/.claude", f.Calls[0].ProfileDir)
	}
	if strings.Join(f.Calls[1].Args, " ") != "plugin list --available --json" {
		t.Errorf("second call = %v, want plugin list", f.Calls[1].Args)
	}
}

// deadlineRecordingRunner records, per space-joined args, whether the call's
// context carried a deadline.
type deadlineRecordingRunner struct {
	hasDeadline map[string]bool
}

func (d *deadlineRecordingRunner) Run(ctx context.Context, _ string, args ...string) ([]byte, error) {
	_, ok := ctx.Deadline()
	d.hasDeadline[strings.Join(args, " ")] = ok
	return []byte(`{"installed":[],"available":[]}`), nil
}

func TestLoadPluginsFreshBoundsRefreshWithOwnDeadline(t *testing.T) {
	// A hung marketplace update must not eat the caller's whole budget and
	// fail the local reads that follow: the refresh gets its own deadline
	// even when the parent context has none.
	r := &deadlineRecordingRunner{hasDeadline: map[string]bool{}}

	if _, _, err := LoadPluginsFresh(t.Context(), r, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.hasDeadline["plugin marketplace update"] {
		t.Error("marketplace update ran without its own deadline")
	}
	if r.hasDeadline["plugin list --available --json"] {
		t.Error("plugin list inherited the refresh deadline, want the parent context")
	}
}

func TestLoadPluginsFreshDuplicateEntryKeepsResolvedVersion(t *testing.T) {
	// A catalog can list the same plugin twice; a later duplicate without a
	// version must not erase the version the first entry resolved.
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace update": {},
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [],
				"available": [
					{"pluginId": "a@m1", "version": "1.2.0", "source": "./a"},
					{"pluginId": "a@m1", "source": "./a"}
				]
			}`)},
			"plugin marketplace list --json": {Stdout: []byte(`[]`)},
		},
	}

	_, lv, err := LoadPluginsFresh(t.Context(), f, "/home/u/.claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := lv.Versions[PluginID{Name: "a", Marketplace: "m1"}]; v != "1.2.0" {
		t.Errorf("a@m1 = %q, want 1.2.0 kept from the first entry", v)
	}
}

func TestLoadPluginsCachedSkipsMarketplaceUpdate(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin list --available --json": {Stdout: []byte(`{
				"installed": [{"id": "a@m1", "version": "1.0.0", "enabled": true}],
				"available": [{"pluginId": "a@m1", "version": "1.2.0", "source": "./a"}]
			}`)},
		},
	}

	data, lv, err := LoadPluginsCached(t.Context(), f, "/home/u/.claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lv.Stale {
		t.Error("Stale = true, want false")
	}
	if len(data.Installed) != 1 {
		t.Errorf("Installed = %+v, want one plugin", data.Installed)
	}
	if v := lv.Versions[PluginID{Name: "a", Marketplace: "m1"}]; v != "1.2.0" {
		t.Errorf("a@m1 = %q, want %q", v, "1.2.0")
	}
	for _, c := range f.Calls {
		if strings.Join(c.Args, " ") == "plugin marketplace update" {
			t.Fatal("cached load ran a marketplace update")
		}
	}
}

func TestLoadPluginsFreshStaleOnRefreshFailure(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace update": {Err: errors.New("marketplace source unreachable")},
			"plugin list --available --json": {Stdout: []byte(`{
				"available": [{"pluginId": "a@m1", "version": "1.0.0", "source": "./a"}]
			}`)},
		},
	}

	_, lv, err := LoadPluginsFresh(t.Context(), f, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lv.Stale {
		t.Error("Stale = false, want true after refresh failure")
	}
	if v := lv.Versions[PluginID{Name: "a", Marketplace: "m1"}]; v != "1.0.0" {
		t.Errorf("a@m1 = %q, want cached %q", v, "1.0.0")
	}
}

func TestLoadPluginsFreshCatalogFileFallback(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, `{
		"name": "m1",
		"plugins": [
			{"name": "branch-ref-plugin", "version": "3.1.4"},
			{"name": "still-no-version"}
		]
	}`)

	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace update": {},
			"plugin list --available --json": {Stdout: []byte(`{
				"available": [
					{"pluginId": "branch-ref-plugin@m1", "source": {"source": "git-subdir", "ref": "main"}},
					{"pluginId": "still-no-version@m1", "source": {"source": "github"}},
					{"pluginId": "versioned@m1", "version": "1.0.0", "source": "./v"}
				]
			}`)},
			"plugin marketplace list --json": {Stdout: []byte(`[
				{"name": "m1", "installLocation": ` + strconv.Quote(dir) + `}
			]`)},
		},
	}

	_, got, err := LoadPluginsFresh(t.Context(), f, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := got.Versions[PluginID{Name: "branch-ref-plugin", Marketplace: "m1"}]; v != "3.1.4" {
		t.Errorf("branch-ref-plugin@m1 = %q, want %q from catalog file", v, "3.1.4")
	}
	if v := got.Versions[PluginID{Name: "still-no-version", Marketplace: "m1"}]; v != "" {
		t.Errorf("still-no-version@m1 = %q, want empty", v)
	}
	if v := got.Versions[PluginID{Name: "versioned", Marketplace: "m1"}]; v != "1.0.0" {
		t.Errorf("versioned@m1 = %q, want %q", v, "1.0.0")
	}
}

func TestLoadPluginsFreshFallbackIsBestEffort(t *testing.T) {
	available := FakeResponse{Stdout: []byte(`{
		"available": [{"pluginId": "a@m1", "source": {"source": "github"}}]
	}`)}

	tests := []struct {
		name        string
		marketplace FakeResponse
	}{
		{
			name:        "marketplace list fails",
			marketplace: FakeResponse{Err: errors.New("boom")},
		},
		{
			name: "plugin's marketplace missing from the list",
			marketplace: FakeResponse{Stdout: []byte(`[
				{"name": "other", "installLocation": "/somewhere"}
			]`)},
		},
		{
			name: "marketplace has an empty install location",
			marketplace: FakeResponse{Stdout: []byte(`[
				{"name": "m1", "installLocation": ""}
			]`)},
		},
		{
			name: "install location has no catalog file",
			marketplace: FakeResponse{Stdout: []byte(`[
				{"name": "m1", "installLocation": "/nonexistent/path"}
			]`)},
		},
		{
			name: "catalog file is malformed",
			marketplace: FakeResponse{Stdout: []byte(`[
				{"name": "m1", "installLocation": "MALFORMED_DIR"}
			]`)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(string(tt.marketplace.Stdout), "MALFORMED_DIR") {
				dir := t.TempDir()
				writeCatalog(t, dir, `{"plugins": [`)
				tt.marketplace.Stdout = []byte(strings.ReplaceAll(
					string(tt.marketplace.Stdout), "MALFORMED_DIR", dir))
			}
			f := &FakeRunner{
				Responses: map[string]FakeResponse{
					"plugin marketplace update":      {},
					"plugin list --available --json": available,
					"plugin marketplace list --json": tt.marketplace,
				},
			}

			_, got, err := LoadPluginsFresh(t.Context(), f, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v := got.Versions[PluginID{Name: "a", Marketplace: "m1"}]; v != "" {
				t.Errorf("a@m1 = %q, want empty", v)
			}
		})
	}
}

func TestLoadPluginsFreshSkipsMarketplaceListWhenComplete(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace update": {},
			"plugin list --available --json": {Stdout: []byte(`{
				"available": [{"pluginId": "a@m1", "version": "1.0.0", "source": "./a"}]
			}`)},
		},
	}

	if _, _, err := LoadPluginsFresh(t.Context(), f, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, call := range f.Calls {
		if strings.Join(call.Args, " ") == "plugin marketplace list --json" {
			t.Error("marketplace list invoked although all versions were resolved")
		}
	}
}

func TestLoadPluginsFreshCatalogLoadError(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin marketplace update":      {},
			"plugin list --available --json": {Err: errors.New("exit status 1")},
		},
	}

	if _, _, err := LoadPluginsFresh(t.Context(), f, ""); err == nil {
		t.Fatal("expected error when plugin list fails, got nil")
	}
}

func TestParseMarketplaceCatalogFixture(t *testing.T) {
	got, err := parseMarketplaceCatalog(readFixture(t, "marketplace_catalog.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"ralphex": "0.17.0", "no-version-plugin": ""}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for name, version := range want {
		if got[name] != version {
			t.Errorf("[%q] = %q, want %q", name, got[name], version)
		}
	}
}
