// Package model holds the pure aggregation logic that turns per-profile CLI
// data into the comparison matrices rendered by the UI.
package model

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/korthane/cpm/internal/claudecli"
)

// CellState is a plugin's presence in one profile.
type CellState int

// Cell states: Absent (not in the profile), Installed (enabled), Disabled.
const (
	Absent CellState = iota
	Installed
	Disabled
)

// PluginCell is one profile's state for a plugin row. Version is empty when
// absent or when the CLI reported the version as unknown. Outdated is set when
// Version is strictly behind the row's LatestVersion.
type PluginCell struct {
	State    CellState
	Version  string
	Outdated bool
}

// PluginRow is one comparison-table row: a plugin identity, its latest
// available version, and one cell per profile (same order as the profile list
// the matrix was built from).
type PluginRow struct {
	ID            claudecli.PluginID
	LatestVersion string
	Cells         []PluginCell
}

// BuildPluginMatrix merges per-profile plugin data into comparison rows: one
// row per plugin installed (or disabled) in at least one profile, sorted by
// marketplace then name. Available-only catalog entries do not create rows.
// latest maps plugin → latest available version and may be nil.
func BuildPluginMatrix(perProfile []claudecli.PluginData, latest map[claudecli.PluginID]string) []PluginRow {
	byID := map[claudecli.PluginID]*PluginRow{}
	for i, data := range perProfile {
		for _, p := range data.Installed {
			row, ok := byID[p.ID]
			if !ok {
				row = &PluginRow{
					ID:            p.ID,
					LatestVersion: latest[p.ID],
					Cells:         make([]PluginCell, len(perProfile)),
				}
				byID[p.ID] = row
			}
			state := Installed
			if !p.Enabled {
				state = Disabled
			}
			row.Cells[i] = PluginCell{
				State:    state,
				Version:  p.Version,
				Outdated: versionLess(p.Version, row.LatestVersion),
			}
		}
	}

	rows := make([]PluginRow, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, *row)
	}
	slices.SortFunc(rows, func(a, b PluginRow) int {
		return cmp.Or(
			cmp.Compare(a.ID.Marketplace, b.ID.Marketplace),
			cmp.Compare(a.ID.Name, b.ID.Name),
		)
	})
	return rows
}

// MergeLatestVersions unions the per-profile resolved latest versions into
// one map for BuildPluginMatrix and reports whether any profile's values are
// stale (its marketplace refresh failed). Profiles refresh independently, so
// the same plugin can carry different versions; the newest one wins and empty
// versions never overwrite a known one.
func MergeLatestVersions(perProfile []claudecli.LatestVersions) (map[claudecli.PluginID]string, bool) {
	latest := map[claudecli.PluginID]string{}
	stale := false
	for _, lv := range perProfile {
		stale = stale || lv.Stale
		for id, v := range lv.Versions {
			if v == "" {
				continue
			}
			if cur, ok := latest[id]; !ok || versionLess(cur, v) {
				latest[id] = v
			}
		}
	}
	return latest, stale
}

// versionLess reports whether version a is strictly older than b. Unknown
// versions (either side empty) are never considered outdated. A leading "v"
// is ignored so an installed "1.5.5" matches a catalog tag "v1.5.5".
func versionLess(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return compareVersions(a, b) < 0
}

func compareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range max(len(as), len(bs)) {
		// A missing segment counts as 0 so "1.2" equals "1.2.0".
		sa, sb := "0", "0"
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		if c := compareSegments(sa, sb); c != 0 {
			return c
		}
	}
	return 0
}

// compareSegments compares one dotted segment: numerically when both sides
// start with a number (so "9" < "10"), with the semver pre-release rule when
// the numbers match ("0-rc1" < "0"), and lexically otherwise — good enough
// for an outdated flag.
func compareSegments(a, b string) int {
	na, sufA, okA := splitSegment(a)
	nb, sufB, okB := splitSegment(b)
	if !okA || !okB {
		return cmp.Compare(a, b)
	}
	if c := cmp.Compare(na, nb); c != 0 {
		return c
	}
	switch {
	case sufA == "":
		if sufB == "" {
			return 0
		}
		return 1 // a release is newer than its own pre-releases
	case sufB == "":
		return -1
	default:
		return cmp.Compare(sufA, sufB)
	}
}

// splitSegment splits "0-rc1" into 0 and "-rc1"; ok is false when the segment
// does not start with a parseable number.
func splitSegment(s string) (n int, suffix string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	return n, s[i:], err == nil
}
