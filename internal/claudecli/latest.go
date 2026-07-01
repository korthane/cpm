package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

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

// RefreshMarketplaces re-fetches every marketplace of a profile from its
// source so that the local catalogs carry current latest versions.
func RefreshMarketplaces(ctx context.Context, r Runner, profileDir string) error {
	_, err := r.Run(ctx, profileDir, "plugin", "marketplace", "update")
	return err
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

// ResolveLatestVersions refreshes all marketplaces and returns the fresh
// latest version per available plugin (user requirement: never trust a stale
// cache). The refresh is best-effort: on failure the cached catalog is used
// and Stale is set so the UI can flag the values. Catalog entries without a
// usable version (branch refs, bare urls) are filled from each marketplace's
// <installLocation>/.claude-plugin/marketplace.json, also best-effort.
func ResolveLatestVersions(ctx context.Context, r Runner, profileDir string) (LatestVersions, error) {
	lv := LatestVersions{Versions: map[PluginID]string{}}
	if err := RefreshMarketplaces(ctx, r, profileDir); err != nil {
		lv.Stale = true
	}

	data, err := LoadPlugins(ctx, r, profileDir)
	if err != nil {
		return LatestVersions{}, err
	}

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
	return lv, nil
}

// fillFromCatalogFiles fills empty entries of versions from the on-disk
// marketplace.json catalogs. Purely best-effort: any failure (marketplace
// list, missing file, bad JSON) leaves the affected entries empty.
func fillFromCatalogFiles(ctx context.Context, r Runner, profileDir string, versions map[PluginID]string) {
	markets, err := ListMarketplaces(ctx, r, profileDir)
	if err != nil {
		return
	}

	locations := make(map[string]string, len(markets))
	for _, m := range markets {
		locations[m.Name] = m.InstallLocation
	}

	catalogs := map[string]map[string]string{}
	for id, version := range versions {
		if version != "" {
			continue
		}
		location, ok := locations[id.Marketplace]
		if !ok {
			continue
		}
		catalog, ok := catalogs[id.Marketplace]
		if !ok {
			catalog = readCatalogFile(location)
			catalogs[id.Marketplace] = catalog
		}
		if v := catalog[id.Name]; v != "" {
			versions[id] = v
		}
	}
}

// readCatalogFile reads <installLocation>/.claude-plugin/marketplace.json and
// returns plugin name → version. Errors yield an empty (non-nil) map so the
// caller caches the miss instead of retrying.
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
