package claudecli

import (
	"context"
	"strings"
)

// MCPServer is one entry from `claude mcp list`: the server name and its
// target (command line or URL).
type MCPServer struct {
	Name   string
	Target string
}

// LoadMCP fetches and parses the MCP servers of one profile. `claude mcp list`
// has no --json mode and runs a health check per server, so this is the slow
// loader — callers run it off the UI thread.
func LoadMCP(ctx context.Context, r Runner, profileDir string) ([]MCPServer, error) {
	out, err := r.Run(ctx, profileDir, "mcp", "list")
	if err != nil {
		return nil, err
	}
	return ParseMCPList(out), nil
}

// ParseMCPList parses the plain-text `claude mcp list` output. Server lines
// look like `name: <cmd-or-url> - <status>`; anything without a `: `
// separator (the `Checking MCP server health…` preamble, blank lines, the
// "No MCP servers configured" message) is skipped rather than treated as an
// error, so output-format noise degrades to fewer rows instead of a failure.
func ParseMCPList(out []byte) []MCPServer {
	var servers []MCPServer
	for line := range strings.SplitSeq(string(out), "\n") {
		name, rest, ok := strings.Cut(line, ": ")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		// The optional health-status suffix is stripped from the target
		// (the comparison view shows presence and target, not health);
		// split on the last " - " so targets containing " - " stay intact.
		target := rest
		if i := strings.LastIndex(rest, " - "); i >= 0 {
			target = rest[:i]
		}
		servers = append(servers, MCPServer{
			Name:   strings.TrimSpace(name),
			Target: strings.TrimSpace(target),
		})
	}
	return servers
}
