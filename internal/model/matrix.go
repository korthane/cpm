// Package model holds the pure aggregation logic that turns per-profile CLI
// data into the comparison matrices rendered by the UI.
package model

import (
	"cmp"
	"regexp"
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
// Version is strictly behind the row's LatestVersion. Scope is the install
// scope the CLI reported ("user", "project", "local"; empty when absent) — the
// UI refuses actions on non-user scopes, which its `--scope user`-pinned CLI
// calls cannot touch. CommitSHA is the commit the shown install came from
// (empty when unknown), the base of its change link.
type PluginCell struct {
	State     CellState
	Version   string
	Outdated  bool
	Scope     string
	CommitSHA string
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
			// A profile can report the same plugin installed at two scopes
			// (e.g. user and project); only "user" is actionable via cpm's
			// --scope user-pinned CLI calls, so it wins a collision instead
			// of whichever scope happened to come last in CLI output.
			if row.Cells[i].State != Absent && row.Cells[i].Scope == "user" && p.Scope != "user" {
				continue
			}
			state := Installed
			if !p.Enabled {
				state = Disabled
			}
			row.Cells[i] = PluginCell{
				State:     state,
				Version:   p.Version,
				Outdated:  IsOutdated(p.Version, row.LatestVersion),
				Scope:     p.Scope,
				CommitSHA: p.CommitSHA,
			}
		}
	}

	rows := make([]PluginRow, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, *row)
	}
	slices.SortFunc(rows, func(a, b PluginRow) int {
		return ComparePluginIDs(a.ID, b.ID)
	})
	return rows
}

// ComparePluginIDs orders plugins by marketplace, then name — the row order
// of BuildPluginMatrix — for slices.SortFunc.
func ComparePluginIDs(a, b claudecli.PluginID) int {
	return cmp.Or(
		cmp.Compare(a.Marketplace, b.Marketplace),
		cmp.Compare(a.Name, b.Name),
	)
}

// MergeLatestVersions unions the per-profile resolved latest versions into
// one map for BuildPluginMatrix and reports whether any profile's values are
// stale (its marketplace refresh failed). Profiles refresh independently, so
// the same plugin can carry different versions; the newest one wins. Empty
// and commit-hash values are skipped: IsOutdated treats them as unknown, and
// a hash would sort above every release and mask its upgrade.
func MergeLatestVersions(perProfile []claudecli.LatestVersions) (map[claudecli.PluginID]string, bool) {
	latest := map[claudecli.PluginID]string{}
	stale := false
	for _, lv := range perProfile {
		stale = stale || lv.Stale
		for id, v := range lv.Versions {
			if v == "" || isCommitHash(v) {
				continue
			}
			if cur, ok := latest[id]; !ok || versionLess(cur, v) {
				latest[id] = v
			}
		}
	}
	return latest, stale
}

// LatestSource returns where the merged latest version of a plugin lives:
// the source of a profile whose own latest equals it (version compare), so
// link and version agree. Among ties a non-stale profile wins, then the
// earlier one. ok is false when latest is empty or no such profile has one.
func LatestSource(perProfile []claudecli.LatestVersions, id claudecli.PluginID,
	latest string) (claudecli.PluginSource, bool) {
	if latest == "" {
		return claudecli.PluginSource{}, false
	}
	var found claudecli.PluginSource
	foundStale, ok := false, false
	for _, lv := range perProfile {
		v := lv.Versions[id]
		if v == "" || compareVersions(v, latest) != 0 {
			continue
		}
		src, has := lv.Sources[id]
		if !has {
			continue
		}
		if !ok || (foundStale && !lv.Stale) {
			found, foundStale, ok = src, lv.Stale, true
		}
	}
	return found, ok
}

// IsOutdated reports whether an installed version is strictly behind latest
// (the PluginCell.Outdated rule). An empty or commit-hash side is unknown,
// never outdated: a hash has no order against a release version.
func IsOutdated(installed, latest string) bool {
	if isCommitHash(installed) || isCommitHash(latest) {
		return false
	}
	return versionLess(installed, latest)
}

var hexRun = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

// isCommitHash requires a hex letter so an all-digit version such as
// "202401" still compares numerically.
func isCommitHash(v string) bool {
	return hexRun.MatchString(v) && strings.ContainsAny(v, "abcdef")
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
