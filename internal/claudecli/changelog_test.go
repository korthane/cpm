package claudecli

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadChangelog(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		path     string
		wantText string
		wantFile string
	}{
		{
			name: "plugin dir wins over root",
			files: map[string]string{
				"plugins/widget/CHANGELOG.md": "## 1.4.0\n",
				"CHANGELOG.md":                "## root\n",
			},
			path:     "./plugins/widget",
			wantText: "## 1.4.0\n", wantFile: "plugins/widget/CHANGELOG.md",
		},
		{
			name:     "root fallback",
			files:    map[string]string{"CHANGELOG.md": "## root\n"},
			path:     "./plugins/widget",
			wantText: "## root\n", wantFile: "CHANGELOG.md",
		},
		{
			name:     "plugin at the clone root",
			files:    map[string]string{"CHANGELOG.md": "## root\n"},
			path:     "./",
			wantText: "## root\n", wantFile: "CHANGELOG.md",
		},
		{
			name:     "path escaping the clone falls back to root",
			files:    map[string]string{"CHANGELOG.md": "## root\n"},
			path:     "../outside",
			wantText: "## root\n", wantFile: "CHANGELOG.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, filepath.Join(dir, name), content)
			}
			text, file, err := ReadChangelog(
				PluginSource{Path: tt.path, CloneDir: dir})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if text != tt.wantText || file != tt.wantFile {
				t.Errorf("got (%q, %q), want (%q, %q)",
					text, file, tt.wantText, tt.wantFile)
			}
		})
	}
}

func TestReadChangelogNone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins", "widget", "README.md"), "x")
	if _, _, err := ReadChangelog(PluginSource{
		Path: "plugins/widget", CloneDir: dir}); err == nil {
		t.Error("expected error when no CHANGELOG.md exists")
	}
}

func TestReadChangelogRemoteSource(t *testing.T) {
	if _, _, err := ReadChangelog(PluginSource{
		RepoURL: "acme/widgets", Path: "plugins/widget"}); err == nil {
		t.Error("expected error for a source without a clone dir")
	}
}

// The clone is third-party content: a changelog symlinked out of it must
// not be followed.
func TestReadChangelogRefusesSymlinkOutOfClone(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside", "CHANGELOG.md")
	writeFile(t, outside, "## secret\n")
	dir := filepath.Join(root, "market")
	if err := os.MkdirAll(filepath.Join(dir, "plugins", "widget"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		filepath.Join(dir, "plugins", "widget", "CHANGELOG.md"),
		filepath.Join(dir, "CHANGELOG.md"),
	} {
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
	}

	text, _, err := ReadChangelog(PluginSource{
		Path: "plugins/widget", CloneDir: dir})
	if err == nil {
		t.Errorf("expected error, got text %q", text)
	}
}
