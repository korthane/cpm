package model

import (
	"regexp"
	"slices"
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
// An empty version is an Unreleased heading: it ends the section above it
// but is never collected.
type versionHeading struct {
	line    int
	scope   string
	version string
}

// ChangelogExcerpt returns the sections of a CHANGELOG.md text whose
// version lies in (installed, latest], newest first whatever the file's
// order; an empty installed takes every version up to latest. When any
// heading is scoped to plugin (`## <plugin> 1.2.0`) only scoped sections are
// collected, else only plain ones; every version heading still ends the
// section above it. ok is false when no collected heading matches latest.
// The result ends in a newline and is capped at changelogMaxLines lines plus
// a truncation marker.
func ChangelogExcerpt(text, plugin, installed, latest string) (string, bool) {
	if latest == "" {
		return "", false
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	headings := findVersionHeadings(lines)
	scoped := slices.ContainsFunc(headings, func(h versionHeading) bool {
		return h.version != "" && strings.EqualFold(h.scope, plugin)
	})
	owns := func(h versionHeading) bool {
		if scoped {
			return strings.EqualFold(h.scope, plugin)
		}
		return h.scope == ""
	}

	type section struct {
		version string
		lines   []string
	}
	var sections []section
	found := false
	for i, h := range headings {
		if h.version == "" || !owns(h) ||
			compareVersions(h.version, latest) > 0 {
			continue
		}
		if installed != "" && compareVersions(h.version, installed) <= 0 {
			continue
		}
		found = found || compareVersions(h.version, latest) == 0
		end := len(lines)
		if i+1 < len(headings) {
			end = headings[i+1].line
		}
		sections = append(sections, section{h.version, lines[h.line:end]})
	}
	if !found {
		return "", false
	}
	// Stable, so a newest-first file keeps its own order.
	slices.SortStableFunc(sections, func(a, b section) int {
		return compareVersions(b.version, a.version)
	})
	var out []string
	for _, s := range sections {
		out = append(out, s.lines...)
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
// word followed by a version; `[1.2.0]` and `[1.2.0](link)` are unwrapped.
// The word is the scope, cut at `@` so `widget@market` scopes to widget;
// "version", "release" and "v" are not scopes. `Unreleased` in place of
// the version gives an empty version.
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
	if v, ok := headingVersionWord(words[0]); ok {
		return versionHeading{version: v}, true
	}
	if len(words) > 1 {
		if v, ok := headingVersionWord(words[1]); ok {
			return versionHeading{scope: headingScope(words[0]), version: v},
				true
		}
	}
	return versionHeading{}, false
}

// headingVersionWord returns the version a heading word names, "" for
// Unreleased.
func headingVersionWord(word string) (string, bool) {
	v := unwrapVersion(word)
	if strings.EqualFold(v, "unreleased") {
		return "", true
	}
	return v, headingVersion.MatchString(v)
}

func headingScope(word string) string {
	switch strings.ToLower(word) {
	case "version", "release", "v":
		return ""
	}
	scope, _, _ := strings.Cut(word, "@")
	return scope
}

func unwrapVersion(word string) string {
	if inner, ok := strings.CutPrefix(word, "["); ok {
		if v, _, found := strings.Cut(inner, "]"); found {
			return v
		}
	}
	return word
}
