package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// installRecord is one install of a plugin in installed_plugins.json.
type installRecord struct {
	Scope        string `json:"scope"`
	InstallPath  string `json:"installPath"`
	GitCommitSHA string `json:"gitCommitSha"`
}

// installedPluginsVersion is the only installed_plugins.json schema cpm
// understands; another one may reuse field names with other meanings.
const installedPluginsVersion = 2

// fillInstalledCommits sets CommitSHA on each installed plugin from the
// profile's plugins/installed_plugins.json, which `plugin list --json` does
// not expose. An install matches the records with the same id, scope and
// install path (compared after resolving symlinks), else the only record of
// that id and scope. The read is best-effort: any failure, no match or an
// ambiguous one leaves CommitSHA empty. cpm only reads this file, never writes it.
func fillInstalledCommits(profileDir string, installed []InstalledPlugin) {
	if len(installed) == 0 {
		return
	}
	records := readInstallRecords(profileDir)
	if records == nil {
		return
	}
	for i := range installed {
		p := &installed[i]
		p.CommitSHA = installedCommit(records[p.ID.String()], *p)
	}
}

// readInstallRecords reads installed_plugins.json, keyed by plugin id; nil
// when the file is missing, unreadable or of an unknown schema.
func readInstallRecords(profileDir string) map[string][]installRecord {
	if profileDir == "" {
		// The runner strips CLAUDE_CONFIG_DIR here, so claude uses ~/.claude.
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		profileDir = filepath.Join(home, ".claude")
	}
	root, err := os.OpenRoot(filepath.Join(profileDir, "plugins"))
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	// Reuse the clone-read hardening: a stray FIFO or huge file must not
	// hang or bloat a load. Real files are a few KiB, far below the cap.
	raw, err := readCloneFile(root, "installed_plugins.json")
	if err != nil {
		return nil
	}
	var file struct {
		Version int                        `json:"version"`
		Plugins map[string][]installRecord `json:"plugins"`
	}
	if json.Unmarshal(raw, &file) != nil ||
		file.Version != installedPluginsVersion {
		return nil
	}
	return file.Plugins
}

// installedCommit returns p's commit SHA from the records of its id. Only
// records with p's scope count: those at p's install path, else the only
// one of that scope. Several path matches may repeat one install, so a SHA
// they agree on wins over a SHA-less record; conflicting SHAs give "".
func installedCommit(records []installRecord, p InstalledPlugin) string {
	var scoped []installRecord
	for _, r := range records {
		if r.Scope == p.Scope {
			scoped = append(scoped, r)
		}
	}
	if p.InstallPath != "" {
		want := resolvePath(p.InstallPath)
		var atPath []installRecord
		for _, r := range scoped {
			if r.InstallPath != "" && resolvePath(r.InstallPath) == want {
				atPath = append(atPath, r)
			}
		}
		if len(atPath) > 0 {
			return agreedCommit(atPath)
		}
	}
	// Records at other paths are other installs: guess only when unique.
	if len(scoped) != 1 {
		return ""
	}
	return scoped[0].GitCommitSHA
}

// agreedCommit returns the non-empty SHA all records share, or "" when
// none has one or two disagree.
func agreedCommit(records []installRecord) string {
	sha := ""
	for _, r := range records {
		switch {
		case r.GitCommitSHA == "":
		case sha == "":
			sha = r.GitCommitSHA
		case sha != r.GitCommitSHA:
			return ""
		}
	}
	return sha
}

// resolvePath returns path with symlinks resolved, or just cleaned when it
// cannot be resolved (e.g. the directory no longer exists).
func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
