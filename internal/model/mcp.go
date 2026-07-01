package model

import (
	"cmp"
	"slices"

	"github.com/korthane/cpm/internal/claudecli"
)

// MCPCell is one profile's state for an MCP server row: whether the server is
// configured there and, if so, its target and health status.
type MCPCell struct {
	Present bool
	Target  string
	Status  string
}

// MCPRow is one comparison-table row: a server name plus one cell per profile
// (same order as the profile list the matrix was built from).
type MCPRow struct {
	Name  string
	Cells []MCPCell
}

// BuildMCPMatrix merges per-profile MCP server lists into comparison rows:
// one row per server name configured in at least one profile, sorted by name.
func BuildMCPMatrix(perProfile [][]claudecli.MCPServer) []MCPRow {
	byName := map[string]*MCPRow{}
	for i, servers := range perProfile {
		for _, s := range servers {
			row, ok := byName[s.Name]
			if !ok {
				row = &MCPRow{Name: s.Name, Cells: make([]MCPCell, len(perProfile))}
				byName[s.Name] = row
			}
			row.Cells[i] = MCPCell{Present: true, Target: s.Target, Status: s.Status}
		}
	}

	rows := make([]MCPRow, 0, len(byName))
	for _, row := range byName {
		rows = append(rows, *row)
	}
	slices.SortFunc(rows, func(a, b MCPRow) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return rows
}
