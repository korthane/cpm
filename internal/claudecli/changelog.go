package claudecli

import (
	"errors"
	"os"
	"path"
	"path/filepath"
)

const changelogName = "CHANGELOG.md"

// ReadChangelog reads the changelog of the plugin at src from its local
// marketplace clone: <Path>/CHANGELOG.md, else the clone-root CHANGELOG.md.
// file is the clone-relative, slash-separated path of the file read, for
// display. Reads are confined to the clone and refuse non-regular or
// oversized files (readCloneFile); a source without a clone dir is an error.
func ReadChangelog(src PluginSource) (text, file string, err error) {
	if src.CloneDir == "" {
		return "", "", errors.New("no local clone for plugin source")
	}
	root, err := os.OpenRoot(src.CloneDir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = root.Close() }()

	candidates := []string{changelogName}
	dir := path.Clean(filepath.ToSlash(src.Path))
	if dir != "." {
		candidates = []string{path.Join(dir, changelogName), changelogName}
	}
	for _, name := range candidates {
		var raw []byte
		raw, err = readCloneFile(root, filepath.FromSlash(name))
		if err == nil {
			return string(raw), name, nil
		}
	}
	return "", "", err
}
