package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/korthane/cpm/internal/claudecli"
)

// forceANSI makes lipgloss emit SGR sequences: under `go test` stdout is not
// a TTY, so the default Ascii profile would strip all styling and reverse
// video could not be observed in View() output. Tests using this must not
// run in parallel (the profile is a package-global).
func forceANSI(t *testing.T) {
	t.Helper()
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })
}

// reverseSGR matches an SGR sequence carrying the reverse-video attribute
// (7), alone (\x1b[7m) or combined with other attributes (\x1b[1;7m).
var reverseSGR = regexp.MustCompile(`\x1b\[(?:[0-9]+;)*7(?:;[0-9]+)*m`)

// reversedPinnedCells collects the ANSI-stripped text of every pinned cell
// (the segment after the pinned separator) rendered in reverse video.
func reversedPinnedCells(view string) []string {
	var cells []string
	for line := range strings.Lines(view) {
		i := strings.LastIndex(line, pinnedSeparator)
		if i < 0 {
			continue
		}
		cell := line[i+len(pinnedSeparator):]
		if reverseSGR.MatchString(cell) {
			cells = append(cells, strings.TrimSpace(ansi.Strip(cell)))
		}
	}
	return cells
}

// assertHighlighted fails unless exactly one pinned cell is reversed and its
// text contains want.
func assertHighlighted(t *testing.T, view, want string) {
	t.Helper()
	cells := reversedPinnedCells(view)
	if len(cells) != 1 {
		t.Fatalf("reversed pinned cells = %q, want exactly one:\n%s", cells, view)
	}
	if !strings.Contains(cells[0], want) {
		t.Errorf("reversed pinned cell = %q, want it to contain %q", cells[0], want)
	}
}

func TestPinnedCellHighlightFollowsSelectionOnPluginsTab(t *testing.T) {
	forceANSI(t)
	p0 := withMarketplace(installedIn("mp", "a", "b"), "mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)

	// Row 0 is the marketplace header (labelStyle composes with reverse).
	assertHighlighted(t, m.View(), "mp")

	// Moving onto a plugin row moves the highlight with it.
	m, _ = press(t, m, "down")
	assertHighlighted(t, m.View(), "a")
	m, _ = press(t, m, "down")
	assertHighlighted(t, m.View(), "b")
}

func TestPinnedCellHighlightTracksScrollWindow(t *testing.T) {
	forceANSI(t)
	// 1 header + 8 plugin rows; height 14 leaves a 3-row window (chrome = 11),
	// so selecting the last row scrolls the window.
	p0 := withMarketplace(
		installedIn("mp", "n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8"),
		"mp", "a1b2c3", "2026-06-28")
	m := modelWithCells(t, &claudecli.FakeRunner{}, p0)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	m = resized.(Model)

	for range 8 {
		m, _ = press(t, m, "down")
	}
	view := m.View()
	assertHighlighted(t, view, "n8")
	if !strings.Contains(ansi.Strip(view), "n8") {
		t.Fatalf("selected row n8 not visible in the scrolled window:\n%s", view)
	}
}

func TestPinnedCellHighlightFollowsSelectionOnMCPTab(t *testing.T) {
	forceANSI(t)
	servers := []claudecli.MCPServer{
		{Name: "exa", Target: "https://mcp.exa.ai/mcp"},
		{Name: "fs", Target: "npx server-fs"},
	}
	m := mcpModelWithServers(t, &claudecli.FakeRunner{}, servers)

	assertHighlighted(t, m.View(), "exa")
	m, _ = press(t, m, "down")
	assertHighlighted(t, m.View(), "fs")
}
