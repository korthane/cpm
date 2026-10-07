package model

import (
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
)

func TestGitHubWebURL(t *testing.T) {
	t.Parallel()
	const want = "https://github.com/acme/widgets"
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"bare owner/repo", "acme/widgets", want, true},
		{"https", "https://github.com/acme/widgets", want, true},
		{"https with .git", "https://github.com/acme/widgets.git", want, true},
		{"https trailing slash", "https://github.com/acme/widgets/", want, true},
		{"scp ssh", "git@github.com:acme/widgets.git", want, true},
		{"ssh url", "ssh://git@github.com/acme/widgets.git", want, true},
		{"host case ignored", "https://GitHub.COM/acme/widgets", want, true},
		{"surrounding space", "  acme/widgets ", want, true},
		{"dots and dashes", "ac-me/wid.gets_2", "https://github.com/ac-me/wid.gets_2", true},
		{"empty", "", "", false},
		{"other host", "https://gitlab.com/acme/widgets", "", false},
		{"other scp host", "git@gitlab.com:acme/widgets.git", "", false},
		{"lookalike host", "https://github.com.evil.example/acme/widgets", "", false},
		{"extra segment", "https://github.com/acme/widgets/tree/main", "", false},
		{"bare extra segment", "acme/widgets/extra", "", false},
		{"owner only", "acme", "", false},
		{"bad characters", "acme/wid gets", "", false},
		{"query", "https://github.com/acme/widgets?x=1", "", false},
		{"owner dotdot", "../widgets", "", false},
		{"repo dot", "acme/.", "", false},
		{"repo only .git", "acme/.git", "", false},
		{"http scheme", "http://github.com/acme/widgets", "", false},
		{"userinfo on https", "https://user@github.com/acme/widgets", "", false},
		{"https port", "https://github.com:443/acme/widgets", "", false},
		{"ssh port", "ssh://git@github.com:22/acme/widgets.git", "", false},
		{"fragment", "https://github.com/acme/widgets#readme", "", false},
		{"query after .git", "https://github.com/acme/widgets.git?x=1", "", false},
		{"userinfo hiding host", "https://github.com@evil.example/acme/widgets", "", false},
		{"userinfo before github", "https://evil.example@github.com/acme/widgets", "", false},
		{"scp host in path", "git@evil.example:github.com/acme/widgets", "", false},
		{"scp double at", "git@github.com@evil.example:acme/widgets", "", false},
		{"bare lookalike host", "github.com.evil.example/acme/widgets", "", false},
		{"prefixed lookalike", "https://evilgithub.com/acme/widgets", "", false},
		{"scp lookalike", "git@evilgithub.com:acme/widgets.git", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := GitHubWebURL(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Errorf("GitHubWebURL(%q) = %q, %v; want %q, %v",
					tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestChangeLinks(t *testing.T) {
	t.Parallel()
	const (
		base      = "https://github.com/acme/widgets"
		installed = "1a2b3c4d5e6f"
		latest    = "5d6e7f8a9b0c"
	)
	src := func(repo, commit, path string) claudecli.PluginSource {
		return claudecli.PluginSource{RepoURL: repo, Commit: commit, Path: path}
	}
	tests := []struct {
		name        string
		src         claudecli.PluginSource
		installed   string
		wantCompare string
		wantHistory string
	}{
		{
			name:        "both links",
			src:         src("acme/widgets", latest, "plugins/widget"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
			wantHistory: base + "/commits/" + latest + "/plugins/widget",
		},
		{
			name:        "no installed sha",
			src:         src("acme/widgets", latest, "plugins/widget"),
			wantHistory: base + "/commits/" + latest + "/plugins/widget",
		},
		{
			name:        "root path has no history",
			src:         src("acme/widgets", latest, "."),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:        "empty path has no history",
			src:         src("acme/widgets", latest, ""),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:        "dot-slash path cleaned",
			src:         src("acme/widgets", latest, "./plugins/x"),
			wantHistory: base + "/commits/" + latest + "/plugins/x",
		},
		{
			name:        "dot-slash root",
			src:         src("acme/widgets", latest, "./"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:        "ref instead of sha",
			src:         src("acme/widgets", "v1.4.0", "plugins/widget"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "...v1.4.0",
			wantHistory: base + "/commits/v1.4.0/plugins/widget",
		},
		{
			name:      "no latest commit",
			src:       src("acme/widgets", "", "plugins/widget"),
			installed: installed,
		},
		{
			name:      "invalid latest commit",
			src:       src("acme/widgets", "abc$def", "plugins/widget"),
			installed: installed,
		},
		{
			name:        "invalid installed commit",
			src:         src("acme/widgets", latest, "plugins/widget"),
			installed:   "not a sha",
			wantHistory: base + "/commits/" + latest + "/plugins/widget",
		},
		{
			name:        "dotdot in path",
			src:         src("acme/widgets", latest, "plugins/../../etc"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:        "absolute path",
			src:         src("acme/widgets", latest, "/plugins/widget"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:        "path segments escaped",
			src:         src("acme/widgets", latest, "plugins/my widget?#"),
			wantHistory: base + "/commits/" + latest + "/plugins/my%20widget%3F%23",
		},
		{
			name:      "dot latest commit refused",
			src:       src("acme/widgets", ".", "plugins/widget"),
			installed: installed,
		},
		{
			name:      "dotdot latest commit refused",
			src:       src("acme/widgets", "..", "plugins/widget"),
			installed: installed,
		},
		{
			name:      "dotdot installed commit ignored",
			src:       src("acme/widgets", latest, ""),
			installed: "..",
		},
		{
			// Percent signs are escaped, so encoded dots cannot traverse.
			name:        "encoded dotdot escaped",
			src:         src("acme/widgets", latest, "plugins/%2e%2e/x"),
			wantHistory: base + "/commits/" + latest + "/plugins/%252e%252e/x",
		},
		{
			name:        "upper encoded dotdot escaped",
			src:         src("acme/widgets", latest, "plugins/%2E%2E/x"),
			wantHistory: base + "/commits/" + latest + "/plugins/%252E%252E/x",
		},
		{
			name:        "encoded slash escaped",
			src:         src("acme/widgets", latest, "plugins/..%2f..%2fx"),
			wantHistory: base + "/commits/" + latest + "/plugins/..%252f..%252fx",
		},
		{
			name:      "ref with slash refused",
			src:       src("acme/widgets", "release/1.4", "plugins/widget"),
			installed: installed,
		},
		{
			name:        "full tag ref shortened",
			src:         src("acme/widgets", "refs/tags/v1.4.0", "plugins/widget"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "...v1.4.0",
			wantHistory: base + "/commits/v1.4.0/plugins/widget",
		},
		{
			name:        "full branch ref shortened",
			src:         src("acme/widgets", "refs/heads/main", "plugins/widget"),
			wantHistory: base + "/commits/main/plugins/widget",
		},
		{
			name:        "inner dot and empty segments cleaned",
			src:         src("acme/widgets", latest, "plugins/./x//y/"),
			wantHistory: base + "/commits/" + latest + "/plugins/x/y",
		},
		{
			name:        "dot-only path is the root",
			src:         src("acme/widgets", latest, "./././"),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:      "ref with triple dot refused",
			src:       src("acme/widgets", "a...b", "plugins/widget"),
			installed: installed,
		},
		{
			name:      "ref with dotdot refused",
			src:       src("acme/widgets", "a..b", "plugins/widget"),
			installed: installed,
		},
		{
			name:      "ref with trailing dot refused",
			src:       src("acme/widgets", "v1.", "plugins/widget"),
			installed: installed,
		},
		{
			name:      "owner dotdot refused",
			src:       src("../widgets", latest, "plugins/widget"),
			installed: installed,
		},
		{
			name:      "non-github repo",
			src:       src("https://gitlab.com/acme/widgets", latest, "plugins/widget"),
			installed: installed,
		},
		{
			name:        "host case ignored",
			src:         src("https://GITHUB.com/acme/widgets.git", latest, ""),
			installed:   installed,
			wantCompare: base + "/compare/" + installed + "..." + latest,
		},
		{
			name:        "installed equals latest",
			src:         src("acme/widgets", latest, "plugins/widget"),
			installed:   latest,
			wantHistory: base + "/commits/" + latest + "/plugins/widget",
		},
		{
			name:        "installed full sha equals latest short",
			src:         src("acme/widgets", "5d6e7f8", "plugins/widget"),
			installed:   "5D6E7F8A9B0C1D2E3F405D6E7F8A9B0C1D2E3F40",
			wantHistory: base + "/commits/5d6e7f8/plugins/widget",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compare, history := ChangeLinks(tt.src, tt.installed)
			if compare != tt.wantCompare {
				t.Errorf("compare = %q, want %q", compare, tt.wantCompare)
			}
			if history != tt.wantHistory {
				t.Errorf("history = %q, want %q", history, tt.wantHistory)
			}
		})
	}
}
