package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

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

// incomplete reports a load whose marketplace list failed: `available`
// omits installed plugins, so they met only other profiles' catalogs.
func (l profileLoad) incomplete() bool {
	return l.err == nil && l.data.MarketplacesUnknown
}

type outdatedInstall struct {
	profile config.Profile
	plugin  claudecli.InstalledPlugin
	// compare links the install's commit to the latest one; "" if unknown.
	compare string
}

type outdatedPlugin struct {
	id       claudecli.PluginID
	latest   string
	installs []outdatedInstall
	// source is where latest lives, from the profile that supplied it.
	source claudecli.PluginSource
	// history lists the latest commits touching the plugin; "" if unknown.
	history string
	// changelog is nil unless --changelog asked for it.
	changelog *pluginChangelog
}

// pluginChangelog is the changelog excerpt of one outdated plugin, or why
// there is none: text and since are empty exactly when missing is set.
type pluginChangelog struct {
	file    string
	since   string
	text    string
	missing string
}

// attachChangelogs reads each plugin's changelog from the clone its latest
// version lives in. The excerpt starts after the oldest install, so every
// install's missing entries are covered.
func attachChangelogs(outdated []outdatedPlugin) {
	for i := range outdated {
		op := &outdated[i]
		since := op.installs[0].plugin.Version
		for _, in := range op.installs[1:] {
			if model.IsOutdated(in.plugin.Version, since) {
				since = in.plugin.Version
			}
		}
		op.changelog = readPluginChangelog(op.source, op.id.Name, since,
			op.latest)
	}
}

func readPluginChangelog(src claudecli.PluginSource,
	plugin, since, latest string) *pluginChangelog {
	// A remote source, or a relative one whose clone was never located
	// because its profile's marketplace list failed.
	if src.CloneDir == "" {
		return &pluginChangelog{missing: "no local changelog"}
	}
	// Any read failure (absent, unreadable, refused) leaves nothing to show.
	text, file, err := claudecli.ReadChangelog(src)
	if err != nil {
		return &pluginChangelog{missing: "no CHANGELOG.md"}
	}
	excerpt, ok := model.ChangelogExcerpt(text, plugin, since, latest)
	if !ok {
		return &pluginChangelog{file: file,
			missing: "no entry for " + quoteControl(latest)}
	}
	return &pluginChangelog{file: file, since: since, text: excerpt}
}

func runOutdated(ctx context.Context, r claudecli.Runner,
	profiles []config.Profile, opts Options, stdout, stderr io.Writer) int {
	loads := loadProfiles(ctx, r, profiles, opts.Refresh)
	outdated := findOutdated(loads)
	if opts.Changelog {
		attachChangelogs(outdated)
	}

	// An unchecked profile is a failure too: nothing proves it is current.
	failed := slices.ContainsFunc(loads,
		func(l profileLoad) bool { return l.err != nil || l.incomplete() })
	labels := profileLabels(profiles)
	var err error
	if opts.Format == FormatJSON {
		err = writeOutdatedJSON(stdout, labels, loads, outdated, opts.Refresh)
	} else {
		err = writeOutdatedText(stdout, stderr, labels, loads, outdated,
			opts.Refresh, failed)
	}
	return ExitCode(failed, err, stderr)
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
				op.source, _ = model.LatestSource(perProfile, p.ID, op.latest)
				_, op.history = model.ChangeLinks(op.source, "")
				byID[p.ID] = op
			}
			compare, _ := model.ChangeLinks(op.source, p.CommitSHA)
			op.installs = append(op.installs, outdatedInstall{
				profile: l.profile, plugin: p, compare: compare})
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

// writeOutdatedText returns the stdout write error; stderr diagnostics are
// best-effort.
func writeOutdatedText(stdout, stderr io.Writer, labels map[string]string,
	loads []profileLoad, outdated []outdatedPlugin,
	refresh, failed bool) error {
	for _, l := range loads {
		label := quoteControl(labels[l.profile.Path])
		switch {
		case l.err != nil:
			_, _ = fmt.Fprintf(stderr, "error: %s: %s\n", label,
				quoteControl(l.err.Error()))
		case l.refreshStatus(refresh) == refreshFailed:
			_, _ = fmt.Fprintf(stderr,
				"warning: %s: marketplace refresh failed; using cached catalog\n",
				label)
		}
		if l.incomplete() {
			_, _ = fmt.Fprintf(stderr, "error: %s: marketplace list failed; "+
				"its catalogs were not read, so outdated plugins may be missed\n",
				label)
		}
	}

	if len(outdated) == 0 {
		// With a failed or unchecked profile nothing proves its plugins are
		// current.
		if !failed {
			_, err := fmt.Fprintln(stdout, "all plugins up to date")
			return err
		}
		return nil
	}

	// One column layout for every group; tabwriter would restart it at each
	// tab-less header line.
	labelWidth, versionWidth := 0, 0
	for _, op := range outdated {
		for _, in := range op.installs {
			// fmt pads by runes, so measure in runes too.
			labelWidth = max(labelWidth, utf8.RuneCountInString(
				quoteControl(labels[in.profile.Path])))
			versionWidth = max(versionWidth, utf8.RuneCountInString(
				quoteControl(in.plugin.Version)))
		}
	}
	// Render into memory so one Write reports any stdout failure.
	var buf bytes.Buffer
	for _, op := range outdated {
		_, _ = fmt.Fprintf(&buf, "%s  latest %s\n",
			quoteControl(op.id.String()), quoteControl(op.latest))
		for _, in := range op.installs {
			label := quoteControl(labels[in.profile.Path])
			version := quoteControl(in.plugin.Version)
			if markers := installMarkers(in.plugin); markers != "" {
				_, _ = fmt.Fprintf(&buf, "  %-*s  %-*s  %s\n",
					labelWidth, label, versionWidth, version, markers)
			} else {
				_, _ = fmt.Fprintf(&buf, "  %-*s  %s\n",
					labelWidth, label, version)
			}
			if in.compare != "" {
				_, _ = fmt.Fprintf(&buf, "    changes: %s\n",
					quoteControl(in.compare))
			}
		}
		if op.history != "" {
			_, _ = fmt.Fprintf(&buf, "  history: %s\n", quoteControl(op.history))
		}
		writeChangelogText(&buf, op.changelog)
	}
	_, err := stdout.Write(buf.Bytes())
	return err
}

func writeChangelogText(buf *bytes.Buffer, c *pluginChangelog) {
	switch {
	case c == nil:
		return
	case c.missing != "":
		_, _ = fmt.Fprintf(buf, "  changelog: %s\n", c.missing)
		return
	}
	_, _ = fmt.Fprintf(buf, "  changelog (%s):\n", quoteControl(c.file))
	for line := range strings.Lines(c.text) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			buf.WriteByte('\n')
			continue
		}
		// Expand tabs first: quoting would mangle a plain indented line.
		line = strings.ReplaceAll(line, "\t", "    ")
		_, _ = fmt.Fprintf(buf, "    %s\n", quoteControl(line))
	}
}

func installMarkers(p claudecli.InstalledPlugin) string {
	var markers []string
	if !p.Enabled {
		markers = append(markers, "(disabled)")
	}
	if p.Scope != "" && p.Scope != "user" {
		markers = append(markers, "(scope: "+quoteControl(p.Scope)+")")
	}
	return strings.Join(markers, " ")
}

type outdatedJSON struct {
	Profiles []profileJSON        `json:"profiles"`
	Outdated []outdatedPluginJSON `json:"outdated"`
}

type profileJSON struct {
	Label      string `json:"label"`
	Path       string `json:"path"`
	Refresh    string `json:"refresh"`
	Error      string `json:"error"`
	Incomplete bool   `json:"incomplete"`
}

type outdatedPluginJSON struct {
	Plugin     string        `json:"plugin"`
	Latest     string        `json:"latest"`
	Installs   []installJSON `json:"installs"`
	HistoryURL string        `json:"history_url"`
	Changelog  changelogJSON `json:"changelog,omitzero"`
}

// changelogJSON renders null when --changelog found nothing, and is omitted
// entirely without the flag.
type changelogJSON struct {
	requested bool
	found     *changelogFoundJSON
}

type changelogFoundJSON struct {
	File  string `json:"file"`
	Since string `json:"since"`
	Text  string `json:"text"`
}

func (c changelogJSON) IsZero() bool { return !c.requested }

func (c changelogJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.found)
}

func newChangelogJSON(c *pluginChangelog) changelogJSON {
	switch {
	case c == nil:
		return changelogJSON{}
	case c.missing != "":
		return changelogJSON{requested: true}
	}
	return changelogJSON{requested: true, found: &changelogFoundJSON{
		File: c.file, Since: c.since, Text: c.text}}
}

type installJSON struct {
	Label      string `json:"label"`
	Path       string `json:"path"`
	Version    string `json:"version"`
	Scope      string `json:"scope"`
	Enabled    bool   `json:"enabled"`
	CompareURL string `json:"compare_url"`
}

func writeOutdatedJSON(stdout io.Writer, labels map[string]string,
	loads []profileLoad, outdated []outdatedPlugin, refresh bool) error {
	// Non-nil slices: the documented shape promises arrays, never null.
	doc := outdatedJSON{
		Profiles: make([]profileJSON, 0, len(loads)),
		Outdated: make([]outdatedPluginJSON, 0, len(outdated)),
	}
	for _, l := range loads {
		p := profileJSON{
			Label:      labels[l.profile.Path],
			Path:       l.profile.Path,
			Refresh:    l.refreshStatus(refresh),
			Incomplete: l.incomplete(),
		}
		if l.err != nil {
			p.Error = l.err.Error()
		}
		doc.Profiles = append(doc.Profiles, p)
	}
	for _, op := range outdated {
		o := outdatedPluginJSON{
			Plugin:     op.id.String(),
			Latest:     op.latest,
			Installs:   make([]installJSON, 0, len(op.installs)),
			HistoryURL: op.history,
			Changelog:  newChangelogJSON(op.changelog),
		}
		for _, in := range op.installs {
			o.Installs = append(o.Installs, installJSON{
				Label:      labels[in.profile.Path],
				Path:       in.profile.Path,
				Version:    in.plugin.Version,
				Scope:      in.plugin.Scope,
				Enabled:    in.plugin.Enabled,
				CompareURL: in.compare,
			})
		}
		doc.Outdated = append(doc.Outdated, o)
	}
	return json.NewEncoder(stdout).Encode(doc)
}
