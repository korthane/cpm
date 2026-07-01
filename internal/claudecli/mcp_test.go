package claudecli

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestParseMCPListFixture(t *testing.T) {
	out, err := os.ReadFile("testdata/mcp_list.txt")
	if err != nil {
		t.Fatal(err)
	}

	servers := ParseMCPList(out)
	if len(servers) != 8 {
		t.Fatalf("got %d servers, want 8:\n%+v", len(servers), servers)
	}

	// The health-status suffix is stripped from every target.
	want := []MCPServer{
		{Name: "claude.ai Exa", Target: "https://mcp.exa.ai/mcp"},
		{Name: "claude.ai Gmail", Target: "https://gmailmcp.googleapis.com/mcp/v1"},
		{Name: "claude.ai Google Drive", Target: "https://drivemcp.googleapis.com/mcp/v1"},
		{Name: "claude.ai Google Calendar", Target: "https://calendarmcp.googleapis.com/mcp/v1"},
		{Name: "plugin:playwright:playwright", Target: "npx @playwright/mcp@latest"},
		{Name: "swifteye", Target: "/Users/alek/src/swifteye/.build/release/swifteye"},
		{Name: "atlassian", Target: "https://mcp.atlassian.com/v1/mcp (HTTP)"},
		{Name: "macos_automator", Target: "npx -y @steipete/macos-automator-mcp@latest"},
	}
	for i, w := range want {
		if servers[i] != w {
			t.Errorf("server[%d] = %+v, want %+v", i, servers[i], w)
		}
	}
}

func TestParseMCPListVariedLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []MCPServer
	}{
		{
			name: "empty output",
			in:   "",
			want: nil,
		},
		{
			name: "no servers message",
			in:   "No MCP servers configured. Use `claude mcp add` to add a server.\n",
			want: nil,
		},
		{
			name: "preamble only",
			in:   "Checking MCP server health…\n\n",
			want: nil,
		},
		{
			name: "failed health check",
			in:   "broken: http://localhost:1 - ✘ Failed to connect\n",
			want: []MCPServer{{Name: "broken", Target: "http://localhost:1"}},
		},
		{
			name: "missing status suffix",
			in:   "plain: /usr/local/bin/server\n",
			want: []MCPServer{{Name: "plain", Target: "/usr/local/bin/server"}},
		},
		{
			name: "garbage lines skipped around a valid one",
			in:   "???\nok: cmd - ✔ Connected\n---\n",
			want: []MCPServer{{Name: "ok", Target: "cmd"}},
		},
		{
			// Pins the last-" - " split: only the status suffix is stripped,
			// not everything after the first " - " inside the command line.
			name: "target containing the separator stays intact",
			in:   "tricky: cmd --flag - value - ✔ Connected\n",
			want: []MCPServer{{Name: "tricky", Target: "cmd --flag - value"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMCPList([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d servers, want %d:\n%+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("server[%d] = %+v, want %+v", i, got[i], w)
				}
			}
		})
	}
}

func TestLoadMCP(t *testing.T) {
	out, err := os.ReadFile("testdata/mcp_list.txt")
	if err != nil {
		t.Fatal(err)
	}
	runner := &FakeRunner{
		Responses: map[string]FakeResponse{
			"mcp list": {Stdout: out},
		},
	}

	servers, err := LoadMCP(t.Context(), runner, "/profile")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 8 {
		t.Errorf("got %d servers, want 8", len(servers))
	}

	if len(runner.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(runner.Calls))
	}
	call := runner.Calls[0]
	if call.ProfileDir != "/profile" {
		t.Errorf("profile dir = %q, want /profile", call.ProfileDir)
	}
	wantArgs := []string{"mcp", "list"}
	if !slices.Equal(call.Args, wantArgs) {
		t.Errorf("args = %v, want %v", call.Args, wantArgs)
	}
}

func TestLoadMCPError(t *testing.T) {
	runner := &FakeRunner{Default: FakeResponse{Err: errors.New("boom")}}

	_, err := LoadMCP(t.Context(), runner, "/profile")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
