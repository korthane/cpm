package ui

import "github.com/korthane/cpm/internal/model"

// rowKind distinguishes the two row types of the grouped plugins view.
type rowKind int

const (
	rowMarketplace rowKind = iota
	rowPlugin
)

// rowRef addresses one visible row of the grouped plugins view: a marketplace
// group header (plugin == -1) or a plugin row within its group.
type rowRef struct {
	kind   rowKind
	group  int
	plugin int
}

// visibleRefs flattens the groups into rendered row order, dropping the
// plugin rows of folded groups. It is derived from (columns, folded) on each
// Update/View instead of cached on the Model: caching would need a rebuild at
// every mutation point (loads, errors, reloads, fold toggles), and one missed
// call site means stale refs indexing rebuilt groups.
func visibleRefs(groups []model.PluginGroup, folded map[string]bool) []rowRef {
	refs := make([]rowRef, 0, len(groups))
	for gi, g := range groups {
		refs = append(refs, rowRef{kind: rowMarketplace, group: gi, plugin: -1})
		if folded[g.Marketplace.Name] {
			continue
		}
		for pi := range g.Plugins {
			refs = append(refs, rowRef{kind: rowPlugin, group: gi, plugin: pi})
		}
	}
	return refs
}
