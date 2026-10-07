package model

import (
	"regexp"
	"strings"
)

const (
	changelogMaxLines  = 200
	changelogTruncated = "… (truncated)"
)

var headingVersion = regexp.MustCompile(
	`^v?[0-9]+(\.[0-9]+)+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// versionHeading is a recognized changelog section heading: plain
// (`## 1.2.0`) when scope is empty, else scoped (`## <scope> 1.2.0`).
type versionHeading struct {
	line    int
	scope   string
	version string
}

// ChangelogExcerpt returns the sections of a CHANGELOG.md text from the
// heading for latest (inclusive) down to the first heading at or below
// installed (exclusive), or to EOF. When any heading is scoped to plugin
// (`## <plugin> 1.2.0`) only scoped sections are collected, else only plain
// ones; every version heading still ends the section above it. ok is false
// when no collected heading matches latest. The result ends in a newline and
// is capped at changelogMaxLines lines plus a truncation marker.
func ChangelogExcerpt(text, plugin, installed, latest string) (string, bool) {
	if latest == "" {
		return "", false
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	headings := findVersionHeadings(lines)
	scoped := false
	for _, h := range headings {
		if strings.EqualFold(h.scope, plugin) {
			scoped = true
			break
		}
	}
	owns := func(h versionHeading) bool {
		if scoped {
			return strings.EqualFold(h.scope, plugin)
		}
		return h.scope == ""
	}

	var out []string
	started := false
	for i, h := range headings {
		if !owns(h) {
			continue
		}
		c := compareVersions(h.version, latest)
		if !started {
			if c != 0 {
				continue
			}
			started = true
		}
		if installed != "" && compareVersions(h.version, installed) <= 0 {
			break
		}
		if c > 0 {
			continue
		}
		end := len(lines)
		if i+1 < len(headings) {
			end = headings[i+1].line
		}
		out = append(out, lines[h.line:end]...)
	}
	if !started {
		return "", false
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) > changelogMaxLines {
		out = append(out[:changelogMaxLines], changelogTruncated)
	}
	return strings.Join(out, "\n") + "\n", true
}

// findVersionHeadings lists the version headings outside fenced code
// blocks, so a fenced example cannot start or end a section.
func findVersionHeadings(lines []string) []versionHeading {
	var (
		headings []versionHeading
		fence    string
	)
	for i, line := range lines {
		trimmed := trimIndent(line)
		if fence != "" {
			if isFenceClose(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if f := fenceOpen(trimmed); f != "" {
			fence = f
			continue
		}
		if h, ok := parseVersionHeading(trimmed); ok {
			h.line = i
			headings = append(headings, h)
		}
	}
	return headings
}

// trimIndent strips up to three leading spaces, the indentation Markdown
// allows before a heading or fence; four would make an indented code block.
func trimIndent(line string) string {
	for range 3 {
		if !strings.HasPrefix(line, " ") {
			break
		}
		line = line[1:]
	}
	return line
}

// fenceOpen returns the opening run of a ``` or ~~~ fence, or "".
func fenceOpen(line string) string {
	for _, c := range []string{"`", "~"} {
		run := line[:len(line)-len(strings.TrimLeft(line, c))]
		if len(run) < 3 {
			continue
		}
		// CommonMark: a backtick in a ``` info string makes the line an
		// inline code span, not a fence.
		if c == "`" && strings.Contains(line[len(run):], "`") {
			return ""
		}
		return run
	}
	return ""
}

// isFenceClose reports whether line closes fence: the same character, at
// least as long, and nothing but whitespace after it.
func isFenceClose(line, fence string) bool {
	rest := strings.TrimLeft(line, fence[:1])
	return len(line)-len(rest) >= len(fence) && strings.TrimSpace(rest) == ""
}

// parseVersionHeading recognizes `#`–`###` + space, then a version or a
// name followed by a version; `[1.2.0]` and `[1.2.0](link)` are unwrapped.
func parseVersionHeading(line string) (versionHeading, bool) {
	hashes := len(line) - len(strings.TrimLeft(line, "#"))
	if hashes < 1 || hashes > 3 || len(line) == hashes {
		return versionHeading{}, false
	}
	if c := line[hashes]; c != ' ' && c != '\t' {
		return versionHeading{}, false
	}
	words := strings.Fields(line[hashes:])
	if len(words) == 0 {
		return versionHeading{}, false
	}
	if v := unwrapVersion(words[0]); headingVersion.MatchString(v) {
		return versionHeading{version: v}, true
	}
	if len(words) > 1 {
		if v := unwrapVersion(words[1]); headingVersion.MatchString(v) {
			return versionHeading{scope: words[0], version: v}, true
		}
	}
	return versionHeading{}, false
}

func unwrapVersion(word string) string {
	if inner, ok := strings.CutPrefix(word, "["); ok {
		if v, _, found := strings.Cut(inner, "]"); found {
			return v
		}
	}
	return word
}
