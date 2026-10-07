package claudecli

import "context"

// StubGitCommitInfo replaces the marketplace git lookup until restore is
// called. It is exported for the external claudecli_test package; tests
// using it must not run in parallel: the lookup is package-global.
func StubGitCommitInfo(
	fn func(ctx context.Context, dir string) (hash, date string, err error),
) (restore func()) {
	orig := gitCommitInfo
	gitCommitInfo = fn
	return func() { gitCommitInfo = orig }
}
