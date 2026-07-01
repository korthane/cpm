package claudecli

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePluginID(t *testing.T) {
	tests := []struct {
		id   string
		want PluginID
	}{
		{"ralphex@ralphex", PluginID{Name: "ralphex", Marketplace: "ralphex"}},
		{"clangd-lsp@claude-plugins-official", PluginID{Name: "clangd-lsp", Marketplace: "claude-plugins-official"}},
		{"no-marketplace", PluginID{Name: "no-marketplace"}},
		{"", PluginID{}},
	}
	for _, tt := range tests {
		if got := ParsePluginID(tt.id); got != tt.want {
			t.Errorf("ParsePluginID(%q) = %+v, want %+v", tt.id, got, tt.want)
		}
	}
}

func TestPluginIDString(t *testing.T) {
	id := PluginID{Name: "ralphex", Marketplace: "ralphex"}
	if got := id.String(); got != "ralphex@ralphex" {
		t.Errorf("String() = %q, want %q", got, "ralphex@ralphex")
	}
	bare := PluginID{Name: "solo"}
	if got := bare.String(); got != "solo" {
		t.Errorf("String() = %q, want %q", got, "solo")
	}
}

func TestLoadPluginsFixture(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]FakeResponse{
			"plugin list --available --json": {Stdout: readFixture(t, "plugin_list_available.json")},
		},
	}

	got, err := LoadPlugins(t.Context(), f, "/home/u/.claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantInstalled := []InstalledPlugin{
		{ID: PluginID{Name: "clangd-lsp", Marketplace: "claude-plugins-official"}, Version: "1.0.0", Enabled: true},
		// version "unknown" is normalized to empty.
		{ID: PluginID{Name: "feature-dev", Marketplace: "claude-plugins-official"}, Version: "", Enabled: true},
		{ID: PluginID{Name: "ralphex", Marketplace: "ralphex"}, Version: "0.17.0", Enabled: true},
		{ID: PluginID{Name: "superpowers", Marketplace: "claude-plugins-official"}, Version: "6.1.0", Enabled: true},
	}
	if len(got.Installed) != len(wantInstalled) {
		t.Fatalf("Installed len = %d, want %d", len(got.Installed), len(wantInstalled))
	}
	for i, want := range wantInstalled {
		if got.Installed[i] != want {
			t.Errorf("Installed[%d] = %+v, want %+v", i, got.Installed[i], want)
		}
	}

	wantAvailable := []AvailablePlugin{
		// git-subdir source with a version tag ref.
		{ID: PluginID{Name: "42crunch-api-security-testing", Marketplace: "claude-plugins-official"}, LatestVersion: "v1.5.5"},
		// git-subdir source whose ref is a branch name, not a version.
		{ID: PluginID{Name: "adobe-for-creativity", Marketplace: "claude-plugins-official"}, LatestVersion: ""},
		// url source without a ref.
		{ID: PluginID{Name: "agentforce-adlc", Marketplace: "claude-plugins-official"}, LatestVersion: ""},
		// string source with a top-level version field.
		{ID: PluginID{Name: "csharp-lsp", Marketplace: "claude-plugins-official"}, LatestVersion: "1.0.0"},
		// github source without a ref.
		{ID: PluginID{Name: "fullstory", Marketplace: "claude-plugins-official"}, LatestVersion: ""},
	}
	if len(got.Available) != len(wantAvailable) {
		t.Fatalf("Available len = %d, want %d", len(got.Available), len(wantAvailable))
	}
	for i, want := range wantAvailable {
		if got.Available[i] != want {
			t.Errorf("Available[%d] = %+v, want %+v", i, got.Available[i], want)
		}
	}
}

func TestLoadPluginsDisabledPlugin(t *testing.T) {
	f := &FakeRunner{Default: FakeResponse{Stdout: []byte(`{
		"installed": [{"id": "dotfiles@olomix-cc-thingz", "version": "0.1.1", "enabled": false}],
		"available": []
	}`)}}

	got, err := LoadPlugins(t.Context(), f, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := InstalledPlugin{
		ID:      PluginID{Name: "dotfiles", Marketplace: "olomix-cc-thingz"},
		Version: "0.1.1",
		Enabled: false,
	}
	if len(got.Installed) != 1 || got.Installed[0] != want {
		t.Errorf("Installed = %+v, want [%+v]", got.Installed, want)
	}
}

func TestLoadPluginsEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		stdout  []byte
		runErr  error
		wantErr bool
	}{
		{
			name:   "empty arrays yield empty data",
			stdout: []byte(`{"installed": [], "available": []}`),
		},
		{
			name:   "missing arrays yield empty data",
			stdout: []byte(`{}`),
		},
		{
			name:    "malformed JSON is an error",
			stdout:  []byte(`{"installed": [`),
			wantErr: true,
		},
		{
			name:    "empty output is an error",
			stdout:  nil,
			wantErr: true,
		},
		{
			name:    "runner failure is an error",
			runErr:  errors.New("spawn failed"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FakeRunner{Default: FakeResponse{Stdout: tt.stdout, Err: tt.runErr}}

			got, err := LoadPlugins(t.Context(), f, "")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got.Installed) != 0 || len(got.Available) != 0 {
				t.Errorf("expected empty data, got %+v", got)
			}
		})
	}
}

func TestLoadPluginsInvokesCorrectCommand(t *testing.T) {
	f := &FakeRunner{Default: FakeResponse{Stdout: []byte(`{}`)}}

	if _, err := LoadPlugins(t.Context(), f, "/tmp/profile-y"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.Calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(f.Calls))
	}
	call := f.Calls[0]
	if call.ProfileDir != "/tmp/profile-y" {
		t.Errorf("ProfileDir = %q, want %q", call.ProfileDir, "/tmp/profile-y")
	}
	if strings.Join(call.Args, " ") != "plugin list --available --json" {
		t.Errorf("Args = %v, want [plugin list --available --json]", call.Args)
	}
}

func TestLoadPluginsPropagatesRunError(t *testing.T) {
	wantErr := &RunError{Args: []string{"plugin", "list", "--available", "--json"}, ExitCode: 1, Err: errors.New("exit status 1")}
	f := &FakeRunner{Default: FakeResponse{Err: wantErr}}

	_, err := LoadPlugins(t.Context(), f, "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
