package claudecli

import (
	"fmt"
	"os"
	"testing"
)

// Loads for the default profile read ~/.claude/plugins/installed_plugins.json;
// point HOME at an empty dir so tests never see the developer's real file.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "cpm-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
