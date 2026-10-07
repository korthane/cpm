package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// openWait bounds how long an opener may run before it counts as having
// opened the page: xdg-open may run the browser in the foreground and only
// exit when the browser does.
const openWait = 5 * time.Second

// errNotGitHubURL refuses URLs outside GitHub: on macOS `open` would also
// launch files and apps, and link parts are third-party data.
var errNotGitHubURL = errors.New("not a GitHub URL")

// openURL opens a URL in the browser; tests swap it so they never launch one.
var openURL = func(ctx context.Context, url string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return openWith(ctx, opener, openWait, url)
}

// openWith runs opener on url, refusing anything that is not an
// https://github.com/ URL. No shell is involved, and the opener's stdio
// stays detached so it cannot write over the Bubble Tea screen. An early
// exit reports the opener's status; one still running after wait is left
// running (and reaped) and reported as success.
func openWith(ctx context.Context, opener string, wait time.Duration,
	url string) error {
	if !strings.HasPrefix(url, "https://github.com/") {
		return fmt.Errorf("refusing to open %q: %w", url, errNotGitHubURL)
	}
	cmd := exec.Command(opener, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// openDoneMsg reports the outcome of opening url.
type openDoneMsg struct {
	url string
	err error
}

// openLink opens url off the event loop.
func openLink(url string) tea.Cmd {
	return func() tea.Msg {
		return openDoneMsg{url: url, err: openURL(context.Background(), url)}
	}
}
