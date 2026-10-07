package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
	"github.com/korthane/cpm/internal/model"
)

// Refresh outcomes reported per profile by `outdated`.
const (
	refreshSkipped = "skipped"
	refreshOK      = "ok"
	refreshFailed  = "failed"
)

type profileLoad struct {
	profile config.Profile
	data    claudecli.PluginData
	latest  claudecli.LatestVersions
	err     error
}

// refreshStatus is "skipped" for an errored profile too: its refresh outcome
// is moot when nothing was loaded.
func (l profileLoad) refreshStatus(refresh bool) string {
	switch {
	case !refresh || l.err != nil:
		return refreshSkipped
	case l.latest.Stale:
		return refreshFailed
	default:
		return refreshOK
	}
}

type outdatedInstall struct {
	profile config.Profile
	plugin  claudecli.InstalledPlugin
}

type outdatedPlugin struct {
	id       claudecli.PluginID
	latest   string
	installs []outdatedInstall
}

func runOutdated(ctx context.Context, r claudecli.Runner,
	profiles []config.Profile, opts Options, stdout, stderr io.Writer) int {
	loads := loadProfiles(ctx, r, profiles, opts.Refresh)
	outdated := findOutdated(loads)

	failed := slices.ContainsFunc(loads,
		func(l profileLoad) bool { return l.err != nil })
	var err error
	if opts.Format == FormatJSON {
		err = writeOutdatedJSON(stdout, loads, outdated, opts.Refresh)
	} else {
		err = writeOutdatedText(stdout, stderr, loads, outdated, opts.Refresh)
	}
	return exitCode(failed, err, stderr)
}

func loadProfiles(ctx context.Context, r claudecli.Runner,
	profiles []config.Profile, refresh bool) []profileLoad {
	load := claudecli.LoadPluginsCached
	if refresh {
		load = claudecli.LoadPluginsFresh
	}
	return mapProfiles(ctx, profiles,
		func(ctx context.Context, p config.Profile) profileLoad {
			data, latest, err := load(ctx, r, p.Path)
			return profileLoad{profile: p, data: data, latest: latest, err: err}
		})
}

// findOutdated checks every installed entry rather than the matrix cells:
// the matrix keeps one cell per profile (user scope wins), which would hide
// an outdated project-scope install beside a current user-scope one.
func findOutdated(loads []profileLoad) []outdatedPlugin {
	var perProfile []claudecli.LatestVersions
	for _, l := range loads {
		if l.err == nil {
			perProfile = append(perProfile, l.latest)
		}
	}
	latest, _ := model.MergeLatestVersions(perProfile)

	byID := map[claudecli.PluginID]*outdatedPlugin{}
	for _, l := range loads {
		if l.err != nil {
			continue
		}
		for _, p := range l.data.Installed {
			if !model.IsOutdated(p.Version, latest[p.ID]) {
				continue
			}
			op, ok := byID[p.ID]
			if !ok {
				op = &outdatedPlugin{id: p.ID, latest: latest[p.ID]}
				byID[p.ID] = op
			}
			op.installs = append(op.installs,
				outdatedInstall{profile: l.profile, plugin: p})
		}
	}

	result := make([]outdatedPlugin, 0, len(byID))
	for _, op := range byID {
		result = append(result, *op)
	}
	slices.SortFunc(result, func(a, b outdatedPlugin) int {
		return model.ComparePluginIDs(a.id, b.id)
	})
	return result
}

func profileLabel(p config.Profile) string {
	if p.Label != "" {
		return p.Label
	}
	return p.Path
}

// writeOutdatedText returns the stdout write error; stderr diagnostics are
// best-effort.
func writeOutdatedText(stdout, stderr io.Writer, loads []profileLoad,
	outdated []outdatedPlugin, refresh bool) error {
	failed := false
	for _, l := range loads {
		label := profileLabel(l.profile)
		switch {
		case l.err != nil:
			failed = true
			_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", label, l.err)
		case l.refreshStatus(refresh) == refreshFailed:
			_, _ = fmt.Fprintf(stderr,
				"warning: %s: marketplace refresh failed; using cached catalog\n",
				label)
		}
	}

	if len(outdated) == 0 {
		// With a failed profile nothing proves its plugins are current.
		if !failed {
			_, err := fmt.Fprintln(stdout, "all plugins up to date")
			return err
		}
		return nil
	}
	// Render into memory so one Write reports any stdout failure.
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	for _, op := range outdated {
		_, _ = fmt.Fprintf(tw, "%s  latest %s\n", op.id, op.latest)
		for _, in := range op.installs {
			line := "  " + profileLabel(in.profile) + "\t" + in.plugin.Version
			if markers := installMarkers(in.plugin); markers != "" {
				line += "\t" + markers
			}
			_, _ = fmt.Fprintln(tw, line)
		}
	}
	_ = tw.Flush()
	if buf.Len() == 0 {
		return nil
	}
	_, err := stdout.Write(buf.Bytes())
	return err
}

func installMarkers(p claudecli.InstalledPlugin) string {
	var markers []string
	if !p.Enabled {
		markers = append(markers, "(disabled)")
	}
	if p.Scope != "" && p.Scope != "user" {
		markers = append(markers, "(scope: "+p.Scope+")")
	}
	return strings.Join(markers, " ")
}

type outdatedJSON struct {
	Profiles []profileJSON        `json:"profiles"`
	Outdated []outdatedPluginJSON `json:"outdated"`
}

type profileJSON struct {
	Label   string `json:"label"`
	Path    string `json:"path"`
	Refresh string `json:"refresh"`
	Error   string `json:"error"`
}

type outdatedPluginJSON struct {
	Plugin   string        `json:"plugin"`
	Latest   string        `json:"latest"`
	Installs []installJSON `json:"installs"`
}

type installJSON struct {
	Label   string `json:"label"`
	Path    string `json:"path"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
}

func writeOutdatedJSON(stdout io.Writer, loads []profileLoad,
	outdated []outdatedPlugin, refresh bool) error {
	// Non-nil slices: the documented shape promises arrays, never null.
	doc := outdatedJSON{
		Profiles: make([]profileJSON, 0, len(loads)),
		Outdated: make([]outdatedPluginJSON, 0, len(outdated)),
	}
	for _, l := range loads {
		p := profileJSON{
			Label:   profileLabel(l.profile),
			Path:    l.profile.Path,
			Refresh: l.refreshStatus(refresh),
		}
		if l.err != nil {
			p.Error = l.err.Error()
		}
		doc.Profiles = append(doc.Profiles, p)
	}
	for _, op := range outdated {
		o := outdatedPluginJSON{
			Plugin:   op.id.String(),
			Latest:   op.latest,
			Installs: make([]installJSON, 0, len(op.installs)),
		}
		for _, in := range op.installs {
			o.Installs = append(o.Installs, installJSON{
				Label:   profileLabel(in.profile),
				Path:    in.profile.Path,
				Version: in.plugin.Version,
				Scope:   in.plugin.Scope,
				Enabled: in.plugin.Enabled,
			})
		}
		doc.Outdated = append(doc.Outdated, o)
	}
	return json.NewEncoder(stdout).Encode(doc)
}
