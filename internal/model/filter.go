package model

import "github.com/sahilm/fuzzy"

// nameMatches reports whether query fuzzy-matches name as a case-insensitive
// subsequence. The query is literal text: no glob or regex meaning.
func nameMatches(query, name string) bool {
	return len(fuzzy.FindNoSort(query, []string{name})) > 0
}

// FilterPluginGroups narrows groups to those matching query, without
// re-ranking: groups and plugins keep their input order, because reordering
// rows under a grouped table is disorienting. A group whose marketplace name
// matches is kept whole; otherwise it is kept only if some plugin name matches,
// and then only with the matching plugins. An empty query returns groups
// unchanged.
func FilterPluginGroups(groups []PluginGroup, query string) []PluginGroup {
	if query == "" {
		return groups
	}

	out := make([]PluginGroup, 0, len(groups))
	for _, g := range groups {
		if nameMatches(query, g.Marketplace.Name) {
			out = append(out, g)
			continue
		}
		var plugins []PluginRow
		for _, p := range g.Plugins {
			if nameMatches(query, p.ID.Name) {
				plugins = append(plugins, p)
			}
		}
		if len(plugins) > 0 {
			out = append(out, PluginGroup{Marketplace: g.Marketplace, Plugins: plugins})
		}
	}
	return out
}

// FilterMCPRows narrows rows to those whose server name matches query, keeping
// the input order. An empty query returns rows unchanged.
func FilterMCPRows(rows []MCPRow, query string) []MCPRow {
	if query == "" {
		return rows
	}

	out := make([]MCPRow, 0, len(rows))
	for _, r := range rows {
		if nameMatches(query, r.Name) {
			out = append(out, r)
		}
	}
	return out
}
