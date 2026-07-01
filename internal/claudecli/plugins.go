package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// PluginID identifies a plugin as name@marketplace.
type PluginID struct {
	Name        string
	Marketplace string
}

// ParsePluginID splits a `name@marketplace` id. An id without `@` yields an
// empty Marketplace.
func ParsePluginID(id string) PluginID {
	name, marketplace, _ := strings.Cut(id, "@")
	return PluginID{Name: name, Marketplace: marketplace}
}

func (id PluginID) String() string {
	if id.Marketplace == "" {
		return id.Name
	}
	return id.Name + "@" + id.Marketplace
}

// InstalledPlugin is a plugin present in a profile. Version is empty when the
// CLI reports it as "unknown". Scope is where the plugin is installed ("user",
// "project", or "local"); non-user scopes are cwd-dependent, so cpm's
// `--scope user`-pinned actions cannot touch them.
type InstalledPlugin struct {
	ID      PluginID
	Version string
	Enabled bool
	Scope   string
}

// AvailablePlugin is a marketplace catalog entry. LatestVersion is empty when
// the catalog carries no version (e.g. a branch ref or a bare url source);
// the marketplace.json fallback in LoadPluginsFresh resolves those.
type AvailablePlugin struct {
	ID            PluginID
	LatestVersion string
}

// PluginData is the parsed output of `plugin list --available --json` for one
// profile.
type PluginData struct {
	Installed []InstalledPlugin
	Available []AvailablePlugin
}

type installedJSON struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	Scope   string `json:"scope"`
}

// availableJSON matches an `available[]` catalog entry. `source` is
// polymorphic in real output: either a plain string path or an object that may
// carry a `ref`; entries with string sources carry a top-level `version`
// instead.
type availableJSON struct {
	PluginID string          `json:"pluginId"`
	Version  string          `json:"version"`
	Source   json.RawMessage `json:"source"`
}

type sourceJSON struct {
	Ref string `json:"ref"`
}

type pluginListJSON struct {
	Installed []installedJSON `json:"installed"`
	Available []availableJSON `json:"available"`
}

// LoadPlugins fetches and parses installed + available plugins for one profile.
func LoadPlugins(ctx context.Context, r Runner, profileDir string) (PluginData, error) {
	out, err := r.Run(ctx, profileDir, "plugin", "list", "--available", "--json")
	if err != nil {
		return PluginData{}, err
	}

	var raw pluginListJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return PluginData{}, fmt.Errorf("parse plugin list: %w", err)
	}

	data := PluginData{}
	for _, p := range raw.Installed {
		version := p.Version
		if version == "unknown" {
			version = ""
		}
		data.Installed = append(data.Installed, InstalledPlugin{
			ID:      ParsePluginID(p.ID),
			Version: version,
			Enabled: p.Enabled,
			Scope:   p.Scope,
		})
	}
	for _, a := range raw.Available {
		data.Available = append(data.Available, AvailablePlugin{
			ID:            ParsePluginID(a.PluginID),
			LatestVersion: latestVersion(a),
		})
	}
	return data, nil
}

// latestVersion resolves a catalog entry's version: the explicit `version`
// field wins; otherwise `source.ref` is used only when it looks like a version
// tag — refs like "main" are branch names, not versions.
func latestVersion(a availableJSON) string {
	if a.Version != "" {
		return a.Version
	}
	var src sourceJSON
	// A string source has no ref; ignore the type mismatch.
	if err := json.Unmarshal(a.Source, &src); err != nil {
		return ""
	}
	if isVersionRef(src.Ref) {
		return src.Ref
	}
	return ""
}

// versionRef matches a version tag: dotted numbers, optionally after a
// leading "v" and optionally with a "-prerelease" suffix (e.g. "1.2.3",
// "v1.5.5", "2.0.0-rc1"). At least one dot is required — a bare leading digit
// is not enough, or branch names like "2024-rework" would be shown as
// versions — and the suffix must be non-empty alphanumeric/dot/hyphen, so
// refs like "1.2-" or "1.2-x!y" stay branches.
var versionRef = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// isVersionRef reports whether ref looks like a version tag rather than a
// branch name.
func isVersionRef(ref string) bool {
	return versionRef.MatchString(ref)
}
