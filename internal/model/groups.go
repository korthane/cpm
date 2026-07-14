package model

import (
	"cmp"
	"slices"

	"github.com/korthane/cpm/internal/claudecli"
)

// MarketplaceCell is one profile's state for a marketplace row. Commit info
// is empty when the loader's git lookup failed or the marketplace is not
// configured in that profile.
type MarketplaceCell struct {
	Configured bool
	CommitHash string
	CommitDate string // YYYY-MM-DD
	// Local marks a directory-source marketplace, whose clone often has no
	// git info to show; the UI renders `local` there instead of a blank
	// freshness cell.
	Local bool
}

// MarketplaceRow is a group header: one marketplace with one cell per
// profile (same order as the profile list the groups were built from).
type MarketplaceRow struct {
	Name string
	// SourceArg is the positional argument for `plugin marketplace add`,
	// resolved across profiles. Empty means no profile knows a usable
	// source, so the marketplace cannot be added elsewhere.
	SourceArg string
	// SourceConflict is set when profiles report different sources for the
	// same marketplace name; add and implicit-install are refused then.
	SourceConflict bool
	Cells          []MarketplaceCell
}

// PluginGroup is one marketplace and the plugin rows that belong to it.
type PluginGroup struct {
	Marketplace MarketplaceRow
	Plugins     []PluginRow
	// HiddenPlugins counts the plugins FilterPluginGroups dropped from this
	// group; it is 0 unfiltered. Marketplace actions still target the whole
	// marketplace, so the UI must be able to say how much a narrowed group
	// is not showing.
	HiddenPlugins int
}

// BuildPluginGroups partitions the plugin comparison matrix by marketplace:
// one group per marketplace configured in at least one profile or referenced
// by an installed plugin. Orphaned plugins (marketplace configured nowhere)
// keep a group with unconfigured cells; plugin-less marketplaces get a group
// with no plugins so update/remove still work. Groups are sorted by name,
// plugins by name within a group.
func BuildPluginGroups(perProfile []claudecli.PluginData, latest map[claudecli.PluginID]string) []PluginGroup {
	rows := BuildPluginMatrix(perProfile, latest)

	byName := map[string]*MarketplaceRow{}
	ensure := func(name string) *MarketplaceRow {
		row, ok := byName[name]
		if !ok {
			row = &MarketplaceRow{Name: name, Cells: make([]MarketplaceCell, len(perProfile))}
			byName[name] = row
		}
		return row
	}

	for i, data := range perProfile {
		for _, mkt := range data.Marketplaces {
			row := ensure(mkt.Name)
			row.Cells[i] = MarketplaceCell{
				Configured: true,
				CommitHash: mkt.CommitHash,
				CommitDate: mkt.CommitDate,
				Local:      mkt.Source == "directory",
			}
			// An unusable source (empty arg) never conflicts with a known
			// one: it means "don't know", not "different".
			arg := mkt.SourceArg()
			switch {
			case arg == "" || row.SourceConflict:
			case row.SourceArg == "":
				row.SourceArg = arg
			case row.SourceArg != arg:
				row.SourceArg = ""
				row.SourceConflict = true
			}
		}
	}
	for _, r := range rows {
		ensure(r.ID.Marketplace)
	}

	groups := make([]PluginGroup, 0, len(byName))
	for _, row := range byName {
		groups = append(groups, PluginGroup{Marketplace: *row})
	}
	slices.SortFunc(groups, func(a, b PluginGroup) int {
		return cmp.Compare(a.Marketplace.Name, b.Marketplace.Name)
	})

	index := make(map[string]int, len(groups))
	for i, g := range groups {
		index[g.Marketplace.Name] = i
	}
	// rows are sorted marketplace-then-name, so appending in order keeps
	// each group's plugins sorted by name.
	for _, r := range rows {
		i := index[r.ID.Marketplace]
		groups[i].Plugins = append(groups[i].Plugins, r)
	}
	return groups
}
