package model

import (
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
)

func TestBuildMCPMatrixUnionAcrossProfiles(t *testing.T) {
	perProfile := [][]claudecli.MCPServer{
		{
			{Name: "exa", Target: "https://mcp.exa.ai/mcp"},
			{Name: "swifteye", Target: "/bin/swifteye"},
		},
		{
			{Name: "exa", Target: "https://mcp.exa.ai/mcp"},
			{Name: "atlassian", Target: "https://mcp.atlassian.com/v1/mcp"},
		},
	}

	rows := BuildMCPMatrix(perProfile)

	wantNames := []string{"atlassian", "exa", "swifteye"}
	if len(rows) != len(wantNames) {
		t.Fatalf("got %d rows, want %d:\n%+v", len(rows), len(wantNames), rows)
	}
	for i, name := range wantNames {
		if rows[i].Name != name {
			t.Errorf("row %d name = %q, want %q (rows must be sorted)", i, rows[i].Name, name)
		}
		if len(rows[i].Cells) != len(perProfile) {
			t.Errorf("row %q has %d cells, want %d", name, len(rows[i].Cells), len(perProfile))
		}
	}

	// atlassian: absent in profile 0, present in profile 1.
	if rows[0].Cells[0].Present {
		t.Error("atlassian should be absent in profile 0")
	}
	if !rows[0].Cells[1].Present || rows[0].Cells[1].Target != "https://mcp.atlassian.com/v1/mcp" {
		t.Errorf("atlassian cell in profile 1 = %+v, want present with target", rows[0].Cells[1])
	}

	// exa: present in both.
	if !rows[1].Cells[0].Present || !rows[1].Cells[1].Present {
		t.Error("exa should be present in both profiles")
	}

	// swifteye: present only in profile 0.
	if !rows[2].Cells[0].Present || rows[2].Cells[1].Present {
		t.Errorf("swifteye cells = %+v, want present only in profile 0", rows[2].Cells)
	}
}

func TestBuildMCPMatrixEdgeCases(t *testing.T) {
	if rows := BuildMCPMatrix(nil); len(rows) != 0 {
		t.Errorf("nil input: got %d rows, want 0", len(rows))
	}
	if rows := BuildMCPMatrix([][]claudecli.MCPServer{nil, nil}); len(rows) != 0 {
		t.Errorf("all-empty profiles: got %d rows, want 0", len(rows))
	}

	single := BuildMCPMatrix([][]claudecli.MCPServer{
		{{Name: "one", Target: "cmd"}},
	})
	if len(single) != 1 || len(single[0].Cells) != 1 || !single[0].Cells[0].Present {
		t.Errorf("single profile: got %+v, want one row with one present cell", single)
	}
}
