package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInitReturnsNoCmd(t *testing.T) {
	if cmd := newModel().Init(); cmd != nil {
		t.Fatalf("Init() = %v, want nil", cmd)
	}
}

func TestViewRendersPlaceholder(t *testing.T) {
	view := newModel().View()
	if !strings.Contains(view, "CPM") {
		t.Fatalf("View() = %q, want it to mention CPM", view)
	}
}

func TestUpdateQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
	} {
		if _, cmd := newModel().Update(key); !isQuit(t, cmd) {
			t.Errorf("Update(%v) did not return tea.Quit", key)
		}
	}
}

func TestUpdateIgnoresOtherKeys(t *testing.T) {
	if _, cmd := newModel().Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}); cmd != nil {
		t.Fatalf("Update(x) returned a command, want nil")
	}
}

// isQuit reports whether cmd is tea.Quit by invoking it and inspecting the
// message; tea.Quit returns a tea.QuitMsg.
func isQuit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
