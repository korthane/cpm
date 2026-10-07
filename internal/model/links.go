package model

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/korthane/cpm/internal/claudecli"
)

const githubHost = "github.com"

var (
	repoPartPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	hexSHAPattern   = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
)

// GitHubWebURL normalizes a repo string (`owner/repo`, an https URL, an
// scp-style `git@github.com:` or an `ssh://` URL) to its GitHub web URL
// `https://github.com/<owner>/<repo>`. Anything not on github.com, or with
// parts outside `[A-Za-z0-9._-]`, yields ok=false.
func GitHubWebURL(raw string) (string, bool) {
	repoPath, ok := githubRepoPath(strings.TrimSpace(raw))
	if !ok {
		return "", false
	}
	repoPath = strings.TrimSuffix(strings.TrimSuffix(repoPath, "/"), ".git")
	owner, repo, ok := strings.Cut(repoPath, "/")
	if !ok || !validRepoPart(owner) || !validRepoPart(repo) {
		return "", false
	}
	return "https://" + githubHost + "/" + owner + "/" + repo, true
}

// githubRepoPath strips the scheme and github.com host from s, returning the
// `owner/repo…` remainder.
func githubRepoPath(s string) (string, bool) {
	if rest, ok := strings.CutPrefix(s, "https://"); ok {
		host, p, _ := strings.Cut(rest, "/")
		return p, strings.EqualFold(host, githubHost)
	}
	if rest, ok := strings.CutPrefix(s, "ssh://"); ok {
		host, p, _ := strings.Cut(rest, "/")
		host = strings.TrimPrefix(host, "git@")
		return p, strings.EqualFold(host, githubHost)
	}
	if strings.Contains(s, "://") {
		return "", false
	}
	if userHost, p, ok := strings.Cut(s, ":"); ok {
		_, host, hasUser := strings.Cut(userHost, "@")
		return p, hasUser && strings.EqualFold(host, githubHost)
	}
	return s, s != ""
}

func validRepoPart(s string) bool {
	return repoPartPattern.MatchString(s) && s != "." && s != ".."
}

// validCommit accepts a hex SHA or a slash-free ref name. A `/` would be
// escaped to `%2F` and dots could break the `a...b` compare syntax.
func validCommit(s string) bool {
	return repoPartPattern.MatchString(s) && !strings.Contains(s, "..") &&
		!strings.HasPrefix(s, ".") && !strings.HasSuffix(s, ".")
}

// sameCommit reports whether two commits name the same revision, treating a
// short hex SHA as equal to a full one it prefixes.
func sameCommit(a, b string) bool {
	if !hexSHAPattern.MatchString(a) || !hexSHAPattern.MatchString(b) {
		return a == b
	}
	a, b = strings.ToLower(a), strings.ToLower(b)
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// ChangeLinks builds GitHub links for the changes up to src's latest commit.
// compare spans installedSHA...src.Commit and is empty unless both commits
// are valid and differ; history lists the latest commits touching src.Path
// and is empty for the repo root or an unsafe path. Both are empty for a
// non-GitHub repo or an invalid latest commit. The parts are third-party
// data and the URL may reach the system opener, so each is validated.
func ChangeLinks(src claudecli.PluginSource, installedSHA string) (compare, history string) {
	base, ok := GitHubWebURL(src.RepoURL)
	if !ok || !validCommit(src.Commit) {
		return "", ""
	}
	if validCommit(installedSHA) && !sameCommit(installedSHA, src.Commit) {
		compare = base + "/compare/" + installedSHA + "..." + src.Commit
	}
	if p, ok := escapedRepoPath(src.Path); ok {
		history = base + "/commits/" + src.Commit + "/" + p
	}
	return compare, history
}

// escapedRepoPath cleans a repo-relative path and escapes each segment.
// It refuses the repo root, absolute paths and any `..` segment.
func escapedRepoPath(p string) (string, bool) {
	for strings.HasPrefix(p, "./") {
		p = strings.TrimPrefix(p, "./")
	}
	if p == "" || strings.HasPrefix(p, "/") {
		return "", false
	}
	segments := strings.Split(p, "/")
	for _, s := range segments {
		if s == ".." {
			return "", false
		}
	}
	p = path.Clean(p)
	if p == "." {
		return "", false
	}
	segments = strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/"), true
}
