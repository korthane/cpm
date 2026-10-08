package claudecli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// refreshTimeout bounds `plugin marketplace update` alone: it is the only
// network call in a load, and a hung refresh (unreachable git remote) must
// fall back to the cached catalog instead of eating the caller's whole
// budget and failing the cheap local reads that follow.
const refreshTimeout = 30 * time.Second

// Marketplace is one entry of `plugin marketplace list --json`.
type Marketplace struct {
	Name            string `json:"name"`
	Source          string `json:"source"` // github | git | directory
	Repo            string `json:"repo"`
	URL             string `json:"url"`
	Path            string `json:"path"`
	InstallLocation string `json:"installLocation"`
	// HeadSHA and CommitDate (YYYY-MM-DD) of the marketplace clone are
	// filled by the loader via git; marketplaces have no version field, so
	// this is the only freshness signal.
	HeadSHA    string `json:"-"`
	CommitDate string `json:"-"`
}

// SourceArg returns the argument `plugin marketplace add` needs to configure
// this marketplace elsewhere, resolved from the source kind. Empty means no
// usable source is known.
func (m Marketplace) SourceArg() string {
	switch m.Source {
	case "github":
		return m.Repo
	case "git":
		return m.URL
	case "directory":
		return m.Path
	}
	return ""
}

// LatestVersions is the resolved latest version per plugin. A missing or empty
// entry means no version could be determined for that plugin.
type LatestVersions struct {
	Versions map[PluginID]string
	// Sources says where each resolved latest version lives; it is set
	// only for plugins with a non-empty version, by the same entry.
	Sources map[PluginID]PluginSource
	// Stale is set when the marketplace refresh failed and Versions therefore
	// come from the previously cached catalogs.
	Stale bool
}

// PluginSource is where a plugin's latest version lives. Any field may be
// empty when unknown; consumers derive links and changelog reads from it.
type PluginSource struct {
	// RepoURL is the raw repo string: `owner/repo`, an https or ssh URL.
	RepoURL string
	// Commit is the SHA (or, failing that, a ref) of the latest version.
	Commit string
	// Path is the plugin's directory relative to the repo root.
	Path string
	// CloneDir is the local marketplace clone holding the plugin; empty
	// for remote sources.
	CloneDir string
}

// ListMarketplaces fetches the marketplaces configured in a profile.
func ListMarketplaces(ctx context.Context, r Runner, profileDir string) ([]Marketplace, error) {
	out, err := r.Run(ctx, profileDir, "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, err
	}
	var markets []Marketplace
	if err := json.Unmarshal(out, &markets); err != nil {
		return nil, fmt.Errorf("parse marketplace list: %w", err)
	}
	return markets, nil
}

// RefreshMarketplaces runs `plugin marketplace update` for the profile,
// bounded only by ctx.
func RefreshMarketplaces(ctx context.Context, r Runner,
	profileDir string) error {
	_, err := r.Run(ctx, profileDir, "plugin", "marketplace", "update")
	return err
}

// LoadPluginsFresh refreshes the profile's marketplaces (user requirement:
// never trust a stale cache) and then loads its plugin data, so the returned
// latest versions are resolved from the fresh catalog with a single
// `plugin list` spawn. Refresh failure — including a hung refresh, which is
// cut off by refreshTimeout — does not fail the load: the cached catalog is
// used and Stale is set so the UI can flag the values.
func LoadPluginsFresh(ctx context.Context, r Runner, profileDir string) (PluginData, LatestVersions, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
	refreshErr := RefreshMarketplaces(refreshCtx, r, profileDir)
	cancel()

	data, lv, err := LoadPluginsCached(ctx, r, profileDir)
	if err != nil {
		return PluginData{}, LatestVersions{}, err
	}
	lv.Stale = refreshErr != nil
	return data, lv, nil
}

// LoadPluginsCached loads the profile's plugin data and resolves latest
// versions from the already-fetched catalogs, skipping the marketplace
// refresh; post-action refreshes use it because the catalog was refreshed
// moments earlier by the initial load.
//
// A plugin's latest version is, in order: the version in its own
// .claude-plugin/plugin.json when its catalog source is a path inside the
// marketplace clone (Claude Code resolves in-repo plugins from it, and
// catalogs often omit or forget to bump the entry version), else the catalog
// entry's `version`, else a version-like `source.ref`. `available` carries
// the CLI's view of the last two; the on-disk catalog at
// <installLocation>/.claude-plugin/marketplace.json supplies the manifest
// override and fills installed plugins, which `available` leaves out.
// File reads are best-effort and confined to the clone (readCloneCatalog).
func LoadPluginsCached(ctx context.Context, r Runner, profileDir string) (PluginData, LatestVersions, error) {
	data, availableSources, err := loadPlugins(ctx, r, profileDir)
	if err != nil {
		return PluginData{}, LatestVersions{}, err
	}

	// Marketplace metadata is best-effort: a failed list leaves Marketplaces
	// nil instead of failing the load, and the catalog files below then
	// cannot be located. MarketplacesUnknown records the failure so the UI
	// can tell "could not read" from "none configured".
	markets, mErr := ListMarketplaces(ctx, r, profileDir)
	if mErr == nil {
		fillCommitInfo(ctx, markets)
		data.Marketplaces = markets
	}
	data.MarketplacesUnknown = mErr != nil
	fillInstalledCommits(profileDir, data.Installed)

	marketByName := make(map[string]Marketplace, len(markets))
	for _, mkt := range markets {
		marketByName[mkt.Name] = mkt
	}
	lv := LatestVersions{
		Versions: map[PluginID]string{},
		Sources:  map[PluginID]PluginSource{},
	}
	for i, a := range data.Available {
		// Catalogs can list the same plugin twice (e.g. one marketplace under
		// two entries); a later duplicate without a version must not erase an
		// already-resolved one.
		if lv.Versions[a.ID] == "" {
			lv.setLatest(a.ID, a.LatestVersion,
				pluginSource(marketByName[a.ID.Marketplace], availableSources[i]))
		}
	}
	for _, p := range data.Installed {
		if _, ok := lv.Versions[p.ID]; !ok {
			lv.Versions[p.ID] = ""
		}
	}
	applyCatalogFiles(markets, &lv)
	return data, lv, nil
}

// setLatest records version for id together with the source it came from.
func (lv *LatestVersions) setLatest(id PluginID, version string,
	src PluginSource) {
	lv.Versions[id] = version
	if version != "" {
		lv.Sources[id] = src
	}
}

// pluginSource describes where a catalog entry's plugin lives. A relative
// string source is a path inside the clone of mkt: Path and CloneDir are
// always kept for the changelog read, while the repo and HEAD SHA are set
// only as a pair, when both are known. An object source is remote: its repo,
// pinned `sha` (else `ref`, which may be a moving branch) and `path`.
func pluginSource(mkt Marketplace, raw json.RawMessage) PluginSource {
	var rel string
	if json.Unmarshal(raw, &rel) == nil {
		src := PluginSource{Path: rel, CloneDir: mkt.InstallLocation}
		repo := mkt.Repo
		if mkt.Source == "git" {
			repo = mkt.URL
		}
		if mkt.Source != "directory" && repo != "" && mkt.HeadSHA != "" {
			src.RepoURL, src.Commit = repo, mkt.HeadSHA
		}
		return src
	}
	var obj sourceJSON
	if json.Unmarshal(raw, &obj) != nil {
		return PluginSource{}
	}
	return PluginSource{
		RepoURL: cmp.Or(obj.URL, obj.Repo),
		Commit:  cmp.Or(obj.SHA, obj.Ref),
		Path:    obj.Path,
	}
}

// catalogVersions is what the on-disk catalog says about one plugin: the
// version from its own plugin.json, and the entry's `version` (or
// version-like `source.ref`), each with the `source` of the entry that
// supplied it.
type catalogVersions struct {
	Manifest       string
	ManifestSource json.RawMessage
	Entry          string
	EntrySource    json.RawMessage
}

// applyCatalogFiles applies the on-disk catalogs of the listed marketplaces
// to lv: a plugin.json version overrides, a catalog entry version only
// fills an empty entry.
func applyCatalogFiles(markets []Marketplace, lv *LatestVersions) {
	for _, mkt := range markets {
		var ids []PluginID
		for id := range lv.Versions {
			if id.Marketplace == mkt.Name {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		catalog := readCloneCatalog(mkt.InstallLocation)
		for _, id := range ids {
			c := catalog[id.Name]
			switch {
			case c.Manifest != "":
				lv.setLatest(id, c.Manifest, pluginSource(mkt, c.ManifestSource))
			case lv.Versions[id] == "":
				lv.setLatest(id, c.Entry, pluginSource(mkt, c.EntrySource))
			}
		}
	}
}

// maxCatalogFileBytes caps a catalog or manifest read; real ones are a few KiB.
const maxCatalogFileBytes = 1 << 20

// readCloneCatalog reads <installLocation>/.claude-plugin/marketplace.json
// and the in-clone plugin.json files it points to; an unreadable or
// malformed catalog yields an empty map.
func readCloneCatalog(installLocation string) map[string]catalogVersions {
	if installLocation == "" {
		return nil
	}
	root, err := os.OpenRoot(installLocation)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	raw, err := readConfinedFile(root,
		filepath.Join(".claude-plugin", "marketplace.json"))
	if err != nil {
		return nil
	}
	entries, err := parseMarketplaceCatalog(raw)
	if err != nil {
		return nil
	}
	byName := make(map[string]catalogVersions, len(entries))
	for _, e := range entries {
		c := byName[e.Name]
		// A duplicate entry must not erase what an earlier one resolved.
		if c.Manifest == "" {
			c.Manifest = manifestVersion(root, e.Source)
			c.ManifestSource = e.Source
		}
		if c.Entry == "" {
			c.Entry = latestVersion(
				availableJSON{Version: e.Version, Source: e.Source})
			c.EntrySource = e.Source
		}
		byName[e.Name] = c
	}
	return byName
}

// readConfinedFile reads a regular file of at most maxCatalogFileBytes
// inside root. Root refuses paths and symlinks that escape it, and anything
// but a small regular file is refused so a FIFO or huge file cannot hang
// the load or exhaust memory.
func readConfinedFile(root *os.Root, name string) ([]byte, error) {
	// Stat before Open: opening a FIFO blocks until a writer appears.
	info, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxCatalogFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxCatalogFileBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", name,
			maxCatalogFileBytes)
	}
	return raw, nil
}

// catalogEntry is one plugin entry of a marketplace.json catalog.
type catalogEntry struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Source  json.RawMessage `json:"source"`
}

// parseMarketplaceCatalog parses a marketplace.json catalog's plugin entries.
func parseMarketplaceCatalog(raw []byte) ([]catalogEntry, error) {
	var catalog struct {
		Plugins []catalogEntry `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("parse marketplace catalog: %w", err)
	}
	return catalog.Plugins, nil
}

// manifestVersion reads the version from the plugin.json of a plugin whose
// catalog source is a relative path inside the clone at root. Other sources
// (remote objects, absolute or escaping paths) yield empty.
func manifestVersion(root *os.Root, source json.RawMessage) string {
	var rel string
	if json.Unmarshal(source, &rel) != nil || rel == "" {
		return ""
	}
	raw, err := readConfinedFile(root,
		filepath.Join(rel, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return ""
	}
	return manifest.Version
}
