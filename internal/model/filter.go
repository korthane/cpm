package model

import (
	"strings"

	"github.com/sahilm/fuzzy"
)

// nameMatches reports whether query fuzzy-matches name as a case-insensitive
// subsequence. The query is literal text: no glob or regex meaning.
func nameMatches(query, name string) bool {
	return len(fuzzy.FindNoSort(query, []string{name})) > 0
}

// NormalizeQuery trims surrounding whitespace: fuzzy matching treats a space as
// a literal rune to find, and no plugin, marketplace or MCP server name contains
// one, so a stray leading or trailing space would silently match nothing. It is
// exported so a caller storing a query can hold the same form the filters match
// on: a query that is whitespace-only here but non-empty to the caller would
// read as an active filter that filters nothing.
func NormalizeQuery(query string) string {
	return strings.TrimSpace(query)
}

// FilterPluginGroups narrows groups to those matching query, without
// re-ranking: groups and plugins keep their input order, because reordering
// rows under a grouped table is disorienting. A group whose marketplace name
// matches is kept whole; otherwise it is kept only if some plugin name matches,
// and then only with the matching plugins. An empty or whitespace-only query
// returns groups unchanged.
func FilterPluginGroups(groups []PluginGroup, query string) []PluginGroup {
	query = NormalizeQuery(query)
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
// the input order. An empty or whitespace-only query returns rows unchanged.
func FilterMCPRows(rows []MCPRow, query string) []MCPRow {
	query = NormalizeQuery(query)
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
