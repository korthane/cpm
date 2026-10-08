package ui

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
)

// unstubbedOpens counts `o` presses that reached openURL without a stub.
var unstubbedOpens atomic.Int32

// Keep tests away from the developer's real ~/.claude and browser: loads may
// read files under HOME, and an unstubbed `o` would launch the real opener.
// Such a press fails the run, since the test itself may not notice.
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
	openURL = func(string) error {
		unstubbedOpens.Add(1)
		return errors.New("test did not stub openURL")
	}
	code := m.Run()
	if n := unstubbedOpens.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "%d test(s) pressed o without stubOpener\n", n)
		code = 1
	}
	_ = os.RemoveAll(home)
	os.Exit(code)
}
