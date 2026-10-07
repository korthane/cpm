package model

import (
	"fmt"
	"strings"
	"testing"
)

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func TestChangelogExcerptPlain(t *testing.T) {
	t.Parallel()
	text := lines(
		"# Changelog",
		"",
		"## Unreleased",
		"- pending work",
		"",
		"## v1.3.0 - 2026-03-01",
		"- three",
		"### Fixed",
		"- a fix",
		"",
		"## 1.2.0",
		"- two",
		"",
		"### [1.1.0]",
		"- one",
		"",
		"## [1.0.0](https://example.com/compare/a...b) (2026-01-01)",
		"- zero",
	)
	tests := []struct {
		name, installed, latest, want string
	}{
		{
			name: "range with sub-heading kept", installed: "1.1.0",
			latest: "1.3.0",
			want: lines("## v1.3.0 - 2026-03-01", "- three", "### Fixed",
				"- a fix", "", "## 1.2.0", "- two"),
		},
		{
			name: "latest inclusive installed exclusive", installed: "1.2.0",
			latest: "1.3.0",
			want: lines("## v1.3.0 - 2026-03-01", "- three", "### Fixed",
				"- a fix"),
		},
		{
			name: "bracketed headings", installed: "1.0.0", latest: "1.1.0",
			want: lines("### [1.1.0]", "- one"),
		},
		{
			name: "linked heading runs to EOF", installed: "0.9.0",
			latest: "1.0.0",
			want: lines("## [1.0.0](https://example.com/compare/a...b)"+
				" (2026-01-01)", "- zero"),
		},
		{
			name: "v prefix mismatch", installed: "v1.1.0", latest: "v1.2.0",
			want: lines("## 1.2.0", "- two"),
		},
		{
			name: "missing segment equals zero", installed: "1.1",
			latest: "1.2",
			want:   lines("## 1.2.0", "- two"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ChangelogExcerpt(text, "widget", tt.installed, tt.latest)
			if !ok {
				t.Fatalf("ok = false, want true")
			}
			if got != tt.want {
				t.Errorf("excerpt:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestChangelogExcerptScoped(t *testing.T) {
	t.Parallel()
	interleaved := lines(
		"# Changelog",
		"## widget 2.0.0",
		"- widget two",
		"## helper 9.0.0",
		"- helper nine",
		"## widget v1.5.0 - 2026-02-01",
		"- widget one five",
		"## helper 8.0.0",
		"- helper eight",
		"## widget 1.0.0",
		"- widget one",
	)
	tests := []struct {
		name, text, plugin, installed, latest, want string
		ok                                          bool
	}{
		{
			name: "next plugin heading ends section", text: interleaved,
			plugin: "widget", installed: "1.5.0", latest: "2.0.0",
			want: lines("## widget 2.0.0", "- widget two"), ok: true,
		},
		{
			name: "other plugin sections skipped", text: interleaved,
			plugin: "widget", installed: "1.0.0", latest: "2.0.0",
			want: lines("## widget 2.0.0", "- widget two",
				"## widget v1.5.0 - 2026-02-01", "- widget one five"),
			ok: true,
		},
		{
			name: "other plugin", text: interleaved,
			plugin: "helper", installed: "8.0.0", latest: "9.0.0",
			want: lines("## helper 9.0.0", "- helper nine"), ok: true,
		},
		{
			name: "scoped name ignores case", text: interleaved,
			plugin: "Widget", installed: "1.5.0", latest: "2.0.0",
			want: lines("## widget 2.0.0", "- widget two"), ok: true,
		},
		{
			name: "other plugin latest never matches plain", text: interleaved,
			plugin: "gadget", installed: "8.0.0", latest: "9.0.0",
		},
		{
			name: "mixed plain and scoped, scoped mode",
			text: lines(
				"## 3.0.0", "- app three",
				"## widget 2.0.0", "- widget two",
				"## 2.5.0", "- app two five",
				"## widget 1.0.0", "- widget one",
			),
			plugin: "widget", installed: "1.0.0", latest: "2.0.0",
			want: lines("## widget 2.0.0", "- widget two"), ok: true,
		},
		{
			name: "mixed plain and scoped, plain mode",
			text: lines(
				"## 2.0.0", "- two",
				"## helper 9.0.0", "- helper nine",
				"## 1.5.0", "- one five",
				"## 1.0.0", "- one",
			),
			plugin: "widget", installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two", "## 1.5.0", "- one five"),
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ChangelogExcerpt(tt.text, tt.plugin, tt.installed,
				tt.latest)
			if ok != tt.ok || got != tt.want {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestChangelogExcerptEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text, installed, latest, want string
		ok                                  bool
	}{
		{
			name:      "latest heading missing",
			text:      lines("## 1.0.0", "- one"),
			installed: "1.0.0", latest: "2.0.0",
		},
		{
			name:      "empty latest",
			text:      lines("## 1.0.0", "- one"),
			installed: "0.9.0",
		},
		{
			name: "installed heading missing stops at first lower",
			text: lines("## 2.0.0", "- two", "## 1.2.0", "- one two",
				"## 1.0.0", "- one"),
			installed: "1.1.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two", "## 1.2.0", "- one two"),
			ok:   true,
		},
		{
			name:      "installed below every heading runs to EOF",
			text:      lines("## 2.0.0", "- two", "## 1.0.0", "- one"),
			installed: "0.1.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two", "## 1.0.0", "- one"),
			ok:   true,
		},
		{
			name:      "CRLF line endings",
			text:      "## 2.0.0\r\n- two\r\n## 1.0.0\r\n- one\r\n",
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two"), ok: true,
		},
		{
			name:      "trailing blank lines trimmed",
			text:      lines("## 2.0.0", "- two", "", "", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two"), ok: true,
		},
		{
			name:      "pre-release below release",
			text:      lines("## 2.0.0", "- two", "## 2.0.0-rc1", "- rc"),
			installed: "2.0.0-rc1", latest: "2.0.0",
			want: lines("## 2.0.0", "- two"), ok: true,
		},
		{
			name: "heading newer than latest skipped",
			text: lines("## 3.0.0", "- three", "## 2.0.0", "- two",
				"## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two"), ok: true,
		},
		{
			name:      "four hashes is not a version heading",
			text:      lines("## 2.0.0", "#### 1.0.0", "- deep", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "#### 1.0.0", "- deep"), ok: true,
		},
		{
			name:      "up to three spaces indent, four is code",
			text:      lines("   ## 2.0.0", "- two", "    ## 1.5.0", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("   ## 2.0.0", "- two", "    ## 1.5.0"), ok: true,
		},
		{
			name:      "hash without space is not a heading",
			text:      lines("##2.0.0", "- two"),
			installed: "1.0.0", latest: "2.0.0",
		},
		{
			name:      "bare number is not a version",
			text:      lines("## 2", "- two"),
			installed: "1", latest: "2",
		},
		{
			name: "backtick fence hides headings",
			text: lines("## 2.0.0", "- two", "```", "# 1.0.0", "```",
				"- after", "## 1.0.0", "- one"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two", "```", "# 1.0.0", "```",
				"- after"),
			ok: true,
		},
		{
			name:      "fenced latest does not count",
			text:      lines("```markdown", "## 2.0.0", "```", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
		},
		{
			name: "tilde fence hides headings",
			text: lines("## 2.0.0", "~~~~", "# 1.0.0", "```", "~~~",
				"# 1.5.0", "~~~~", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "~~~~", "# 1.0.0", "```", "~~~",
				"# 1.5.0", "~~~~"),
			ok: true,
		},
		{
			name:      "fenced tilde latest does not count",
			text:      lines("~~~", "## 2.0.0", "~~~", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
		},
		{
			name: "inline code span is not a fence",
			text: lines("```example```", "", "## 2.0.0", "- two",
				"## 1.0.0", "- one"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two"), ok: true,
		},
		{
			name: "tilde fence info string may hold backticks",
			text: lines("## 2.0.0", "- two", "~~~ `info`", "## 1.0.0",
				"~~~", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two", "~~~ `info`", "## 1.0.0",
				"~~~"),
			ok: true,
		},
		{
			name: "newer heading after latest skipped",
			text: lines("## 2.0.0", "- two", "## 3.0.0", "- three",
				"## 1.5.0", "- one five", "## 1.0.0"),
			installed: "1.0.0", latest: "2.0.0",
			want: lines("## 2.0.0", "- two", "## 1.5.0", "- one five"),
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ChangelogExcerpt(tt.text, "widget", tt.installed,
				tt.latest)
			if ok != tt.ok || got != tt.want {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestChangelogExcerptTruncates(t *testing.T) {
	t.Parallel()
	body := make([]string, 0, 300)
	body = append(body, "## 2.0.0")
	for i := range 299 {
		body = append(body, fmt.Sprintf("- item %d", i))
	}
	body = append(body, "## 1.0.0")
	got, ok := ChangelogExcerpt(lines(body...), "widget", "1.0.0", "2.0.0")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := lines(append(body[:changelogMaxLines:changelogMaxLines],
		"… (truncated)")...)
	if got != want {
		t.Errorf("got %d lines, want %d", strings.Count(got, "\n"),
			strings.Count(want, "\n"))
	}
}

func TestChangelogExcerptExactlyAtCap(t *testing.T) {
	t.Parallel()
	body := []string{"## 2.0.0"}
	for i := range changelogMaxLines - 1 {
		body = append(body, fmt.Sprintf("- item %d", i))
	}
	got, ok := ChangelogExcerpt(lines(body...), "widget", "1.0.0", "2.0.0")
	if !ok || got != lines(body...) {
		t.Errorf("excerpt at the cap must not be truncated")
	}
}
