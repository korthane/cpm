package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
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
	InstallLocation string `json:"installLocation"`
}

// LatestVersions is the resolved latest version per plugin. A missing or empty
// entry means no version could be determined for that plugin.
type LatestVersions struct {
	Versions map[PluginID]string
	// Stale is set when the marketplace refresh failed and Versions therefore
	// come from the previously cached catalogs.
	Stale bool
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

// LoadPluginsFresh refreshes the profile's marketplaces (user requirement:
// never trust a stale cache) and then loads its plugin data, so the returned
// latest versions are resolved from the fresh catalog with a single
// `plugin list` spawn. Refresh failure — including a hung refresh, which is
// cut off by refreshTimeout — does not fail the load: the cached catalog is
// used and Stale is set so the UI can flag the values.
func LoadPluginsFresh(ctx context.Context, r Runner, profileDir string) (PluginData, LatestVersions, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
	_, refreshErr := r.Run(refreshCtx, profileDir, "plugin", "marketplace", "update")
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
// moments earlier by the initial load. Catalog entries without a usable
// version (branch refs, bare urls) are filled from each marketplace's
// <installLocation>/.claude-plugin/marketplace.json, best-effort.
func LoadPluginsCached(ctx context.Context, r Runner, profileDir string) (PluginData, LatestVersions, error) {
	data, err := LoadPlugins(ctx, r, profileDir)
	if err != nil {
		return PluginData{}, LatestVersions{}, err
	}

	lv := LatestVersions{Versions: map[PluginID]string{}}
	unresolved := false
	for _, a := range data.Available {
		lv.Versions[a.ID] = a.LatestVersion
		if a.LatestVersion == "" {
			unresolved = true
		}
	}
	if unresolved {
		fillFromCatalogFiles(ctx, r, profileDir, lv.Versions)
	}
	return data, lv, nil
}

// fillFromCatalogFiles fills empty entries of versions from the on-disk
// marketplace.json catalogs. Purely best-effort: any failure (marketplace
// list, missing file, bad JSON) leaves the affected entries empty.
func fillFromCatalogFiles(ctx context.Context, r Runner, profileDir string, versions map[PluginID]string) {
	markets, err := ListMarketplaces(ctx, r, profileDir)
	if err != nil {
		return
	}

	for _, mkt := range markets {
		var missing []PluginID
		for id, version := range versions {
			if version == "" && id.Marketplace == mkt.Name {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			continue
		}
		catalog := readCatalogFile(mkt.InstallLocation)
		for _, id := range missing {
			if v := catalog[id.Name]; v != "" {
				versions[id] = v
			}
		}
	}
}

// readCatalogFile reads <installLocation>/.claude-plugin/marketplace.json and
// returns plugin name → version; an unreadable or malformed catalog yields an
// empty map.
func readCatalogFile(installLocation string) map[string]string {
	byName := map[string]string{}
	if installLocation == "" {
		return byName
	}
	raw, err := os.ReadFile(filepath.Join(installLocation, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return byName
	}
	parsed, err := parseMarketplaceCatalog(raw)
	if err != nil {
		return byName
	}
	return parsed
}

// parseMarketplaceCatalog parses a marketplace.json catalog into plugin
// name → version (empty when the entry has no version field).
func parseMarketplaceCatalog(raw []byte) (map[string]string, error) {
	var catalog struct {
		Plugins []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("parse marketplace catalog: %w", err)
	}
	byName := make(map[string]string, len(catalog.Plugins))
	for _, p := range catalog.Plugins {
		byName[p.Name] = p.Version
	}
	return byName, nil
}
