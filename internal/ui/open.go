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

// openTimeout bounds the system opener; it only hands the URL to the
// browser, so anything slower is hung.
const openTimeout = 5 * time.Second

// errNotGitHubURL refuses URLs outside GitHub: on macOS `open` would also
// launch files and apps, and link parts are third-party data.
var errNotGitHubURL = errors.New("not a GitHub URL")

// openerCommand is the system URL opener.
var openerCommand = defaultOpener()

func defaultOpener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// openURL opens a URL in the browser; tests swap it so they never launch one.
var openURL = openInBrowser

// openInBrowser runs the system opener on url, refusing anything that is not
// an https://github.com/ URL. No shell is involved, and the opener's stdio
// stays detached so it cannot write over the Bubble Tea screen.
func openInBrowser(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "https://github.com/") {
		return fmt.Errorf("refusing to open %q: %w", url, errNotGitHubURL)
	}
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	return exec.CommandContext(ctx, openerCommand, url).Run()
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
