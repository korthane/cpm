package claudecli

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"testing"
)

// stubGitCommitInfo swaps the package-level gitCommitInfo for the test's
// duration. Tests using it must not run in parallel.
func stubGitCommitInfo(t *testing.T, fn func(ctx context.Context, dir string) (string, string, error)) {
	t.Helper()
	orig := gitCommitInfo
	gitCommitInfo = fn
	t.Cleanup(func() { gitCommitInfo = orig })
}

func TestFillCommitInfo(t *testing.T) {
	var dirs []string
	stubGitCommitInfo(t, func(_ context.Context, dir string) (string, string, error) {
		dirs = append(dirs, dir)
		if dir == "/loc/broken" {
			return "", "", errors.New("not a git repository")
		}
		return "abc1234", "2026-06-28", nil
	})

	markets := []Marketplace{
		{Name: "m1", InstallLocation: "/loc/m1"},
		{Name: "no-location"},
		{Name: "broken", InstallLocation: "/loc/broken"},
	}
	fillCommitInfo(t.Context(), markets)

	if markets[0].CommitHash != "abc1234" || markets[0].CommitDate != "2026-06-28" {
		t.Errorf("m1 = %+v, want commit abc1234 2026-06-28", markets[0])
	}
	if markets[1].CommitHash != "" || markets[1].CommitDate != "" {
		t.Errorf("no-location = %+v, want blank commit fields", markets[1])
	}
	if markets[2].CommitHash != "" || markets[2].CommitDate != "" {
		t.Errorf("broken = %+v, want blank commit fields on git failure", markets[2])
	}
	wantDirs := []string{"/loc/m1", "/loc/broken"}
	if len(dirs) != len(wantDirs) {
		t.Fatalf("gitCommitInfo dirs = %v, want %v", dirs, wantDirs)
	}
	for i := range wantDirs {
		if dirs[i] != wantDirs[i] {
			t.Errorf("gitCommitInfo dir[%d] = %q, want %q", i, dirs[i], wantDirs[i])
		}
	}
}

func TestGitCommitInfoRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=test@example.com", "-c", "user.name=test",
			"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	hash, date, err := gitCommitInfo(t.Context(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{7,}$`).MatchString(hash) {
		t.Errorf("hash = %q, want abbreviated commit hash", hash)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(date) {
		t.Errorf("date = %q, want YYYY-MM-DD", date)
	}
}

func TestGitCommitInfoNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, _, err := gitCommitInfo(t.Context(), t.TempDir()); err == nil {
		t.Fatal("expected error for a non-repo directory, got nil")
	}
}
