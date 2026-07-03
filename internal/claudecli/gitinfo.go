package claudecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitCommitInfo returns the short hash and committer date (YYYY-MM-DD) of the
// HEAD commit of the git clone at dir. It execs git directly rather than going
// through Runner: this is not a `claude` invocation and needs no
// CLAUDE_CONFIG_DIR. The context bounds it with the caller's load budget. A
// package var so tests can stub it.
var gitCommitInfo = func(ctx context.Context, dir string) (hash, date string, err error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1", "--format=%h %cs")
	// Without a ceiling, repo discovery walks up from dir, so a
	// directory-source marketplace nested inside a larger repo would report
	// the enclosing repo's HEAD as the marketplace's freshness. Only a repo
	// rooted at dir itself counts; anything else stays a blank cell.
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	hash, date, _ = strings.Cut(strings.TrimSpace(string(out)), " ")
	return hash, date, nil
}

// fillCommitInfo stamps each marketplace with its clone's commit hash and
// date — marketplaces have no version field, so this is the only freshness
// signal. Best-effort: any git failure (directory source that is not a repo,
// git missing) leaves the fields blank.
func fillCommitInfo(ctx context.Context, markets []Marketplace) {
	for i := range markets {
		if markets[i].InstallLocation == "" {
			continue
		}
		hash, date, err := gitCommitInfo(ctx, markets[i].InstallLocation)
		if err != nil {
			continue
		}
		markets[i].CommitHash = hash
		markets[i].CommitDate = date
	}
}
