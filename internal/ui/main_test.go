package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

// Keep tests away from the developer's real ~/.claude and browser: loads may
// read files under HOME, and an unstubbed `o` would launch the real opener.
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
	openURL = func(context.Context, string) error {
		return errors.New("test did not stub openURL")
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
