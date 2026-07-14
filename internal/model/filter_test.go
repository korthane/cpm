package model_test

import (
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/model"
)

func group(marketplace string, plugins ...string) model.PluginGroup {
	g := model.PluginGroup{Marketplace: model.MarketplaceRow{Name: marketplace}}
	for _, p := range plugins {
		g.Plugins = append(g.Plugins, model.PluginRow{
			ID: claudecli.PluginID{Name: p, Marketplace: marketplace},
		})
	}
	return g
}

// shape renders groups as marketplace/plugin names so a filter result can be
// compared without spelling out the whole struct.
func shape(groups []model.PluginGroup) []string {
	out := []string{}
	for _, g := range groups {
		out = append(out, g.Marketplace.Name)
		for _, p := range g.Plugins {
			out = append(out, "  "+p.ID.Name)
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterPluginGroups(t *testing.T) {
	t.Parallel()

	groups := []model.PluginGroup{
		group("alpha-market", "foo-bar", "zebra"),
		group("beta-market", "Foo-Baz", "quux"),
		group("empty-market"),
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "empty query is identity",
			query: "",
			want: []string{
				"alpha-market", "  foo-bar", "  zebra",
				"beta-market", "  Foo-Baz", "  quux",
				"empty-market",
			},
		},
		{
			name:  "subsequence match keeps only matching plugins",
			query: "fb",
			want: []string{
				"alpha-market", "  foo-bar",
				"beta-market", "  Foo-Baz",
			},
		},
		{
			name:  "match is case-insensitive both ways",
			query: "FOOBAZ",
			want:  []string{"beta-market", "  Foo-Baz"},
		},
		{
			name:  "marketplace name match keeps the whole group",
			query: "alpha",
			want:  []string{"alpha-market", "  foo-bar", "  zebra"},
		},
		{
			name:  "plugin-less marketplace matches on its own name",
			query: "empty",
			want:  []string{"empty-market"},
		},
		{
			name:  "no match anywhere returns empty",
			query: "nothinghere",
			want:  []string{},
		},
		{
			name:  "order is preserved, never re-ranked by score",
			query: "a",
			want: []string{
				"alpha-market", "  foo-bar", "  zebra",
				"beta-market", "  Foo-Baz", "  quux",
				"empty-market",
			},
		},
		{
			name:  "metacharacters are literal text, not patterns",
			query: ".*",
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shape(model.FilterPluginGroups(groups, tt.query))
			if !equal(got, tt.want) {
				t.Errorf("FilterPluginGroups(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestFilterPluginGroupsDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	groups := []model.PluginGroup{group("alpha-market", "foo-bar", "zebra")}
	model.FilterPluginGroups(groups, "foo")

	want := []string{"alpha-market", "  foo-bar", "  zebra"}
	if got := shape(groups); !equal(got, want) {
		t.Errorf("input mutated: %v, want %v", got, want)
	}
}

func TestFilterMCPRows(t *testing.T) {
	t.Parallel()

	rows := []model.MCPRow{
		{Name: "context7"},
		{Name: "Playwright"},
		{Name: "postgres"},
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "empty query is identity", query: "", want: []string{"context7", "Playwright", "postgres"}},
		{name: "subsequence match", query: "pgs", want: []string{"postgres"}},
		{name: "case-insensitive", query: "PLAY", want: []string{"Playwright"}},
		{name: "order preserved", query: "t", want: []string{"context7", "Playwright", "postgres"}},
		{name: "no match", query: "zzz", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := []string{}
			for _, r := range model.FilterMCPRows(rows, tt.query) {
				got = append(got, r.Name)
			}
			if !equal(got, tt.want) {
				t.Errorf("FilterMCPRows(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
