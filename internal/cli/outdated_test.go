package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

const (
	pluginListKey  = "plugin list --available --json"
	marketListKey  = "plugin marketplace list --json"
	marketUpdateKy = "plugin marketplace update"
)

type fakeInstall struct {
	id, version, scope string
	enabled            bool
}

// pluginList renders a `plugin list --available --json` response. An empty
// version is reported as "unknown", as the real CLI does.
func pluginList(installed []fakeInstall, available map[string]string) []byte {
	type inst struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Scope   string `json:"scope"`
		Enabled bool   `json:"enabled"`
	}
	type avail struct {
		PluginID string `json:"pluginId"`
		Version  string `json:"version"`
	}
	var doc struct {
		Installed []inst  `json:"installed"`
		Available []avail `json:"available"`
	}
	for _, p := range installed {
		v := p.version
		if v == "" {
			v = "unknown"
		}
		doc.Installed = append(doc.Installed,
			inst{ID: p.id, Version: v, Scope: p.scope, Enabled: p.enabled})
	}
	for id, v := range available {
		doc.Available = append(doc.Available, avail{PluginID: id, Version: v})
	}
	out, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return out
}

var (
	homeProfile = config.Profile{Path: "/p/home", Label: "home"}
	workProfile = config.Profile{Path: "/p/work", Label: "work"}
)

// newOutdatedRunner answers `plugin list` per profile dir. The marketplace
// list stays empty so no load execs git against an installLocation.
func newOutdatedRunner(lists map[string][]byte) *claudecli.FakeRunner {
	r := &claudecli.FakeRunner{
		Responses: map[string]claudecli.FakeResponse{
			marketListKey: {Stdout: []byte("[]")},
		},
		ResponsesByDir: map[string]map[string]claudecli.FakeResponse{},
	}
	for dir, out := range lists {
		r.ResponsesByDir[dir] = map[string]claudecli.FakeResponse{
			pluginListKey: {Stdout: out},
		}
	}
	return r
}

func runCmd(t *testing.T, r claudecli.Runner, profiles []config.Profile,
	opts Options) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), r, profiles, opts, &out, &errOut)
	return code, out.String(), errOut.String()
}

type jsonOutdated struct {
	Profiles []struct {
		Label      string `json:"label"`
		Path       string `json:"path"`
		Refresh    string `json:"refresh"`
		Error      string `json:"error"`
		Incomplete bool   `json:"incomplete"`
	} `json:"profiles"`
	Outdated []struct {
		Plugin   string `json:"plugin"`
		Latest   string `json:"latest"`
		Installs []struct {
			Label   string `json:"label"`
			Path    string `json:"path"`
			Version string `json:"version"`
			Scope   string `json:"scope"`
			Enabled bool   `json:"enabled"`
		} `json:"installs"`
	} `json:"outdated"`
}

func decodeOutdated(t *testing.T, stdout string) jsonOutdated {
	t.Helper()
	var doc jsonOutdated
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	return doc
}

// mixedRunner covers every install kind across two profiles: foo outdated in
// both, bar disabled-outdated in work, baz current, qux unknown version, and
// zed outdated at project scope.
func mixedRunner() *claudecli.FakeRunner {
	catalog := map[string]string{
		"foo@acme": "0.35.1", "bar@acme": "6.4.1", "baz@acme": "2.0.0",
		"qux@acme": "9.9.9", "zed@other": "3.0.0",
	}
	return newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "0.34.0", scope: "user", enabled: true},
			{id: "baz@acme", version: "2.0.0", scope: "user", enabled: true},
			{id: "qux@acme", version: "", scope: "user", enabled: true},
		}, catalog),
		workProfile.Path: pluginList([]fakeInstall{
			{id: "zed@other", version: "2.0.0", scope: "project", enabled: true},
			{id: "foo@acme", version: "0.34.0", scope: "user", enabled: true},
			{id: "bar@acme", version: "6.3.0", scope: "user", enabled: false},
		}, catalog),
	})
}

func TestOutdatedText(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, mixedRunner(),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatText})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	want := `bar@acme  latest 6.4.1
  work  6.3.0   (disabled)
foo@acme  latest 0.35.1
  home  0.34.0
  work  0.34.0
zed@other  latest 3.0.0
  work  2.0.0   (scope: project)
`
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestOutdatedJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, mixedRunner(),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	doc := decodeOutdated(t, stdout)
	if len(doc.Profiles) != 2 || doc.Profiles[0].Label != "home" ||
		doc.Profiles[1].Path != "/p/work" {
		t.Errorf("profiles = %+v", doc.Profiles)
	}
	for _, p := range doc.Profiles {
		if p.Refresh != "skipped" || p.Error != "" {
			t.Errorf("profile %s: refresh %q error %q", p.Label, p.Refresh, p.Error)
		}
	}
	var got []string
	for _, o := range doc.Outdated {
		for _, in := range o.Installs {
			got = append(got, fmt.Sprintf("%s %s %s:%s %s %v",
				o.Plugin, o.Latest, in.Label, in.Path, in.Version+"/"+in.Scope,
				in.Enabled))
		}
	}
	want := []string{
		"bar@acme 6.4.1 work:/p/work 6.3.0/user false",
		"foo@acme 0.35.1 home:/p/home 0.34.0/user true",
		"foo@acme 0.35.1 work:/p/work 0.34.0/user true",
		"zed@other 3.0.0 work:/p/work 2.0.0/project true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("installs:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestOutdatedReportsEachScopeOfOneProfile(t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
			{id: "foo@acme", version: "0.9.0", scope: "project", enabled: true},
		}, map[string]string{"foo@acme": "1.0.0"}),
	})
	profiles := []config.Profile{homeProfile}

	_, stdout, _ := runCmd(t, r, profiles,
		Options{Command: "outdated", Format: FormatText})
	want := "foo@acme  latest 1.0.0\n  home  0.9.0  (scope: project)\n"
	if stdout != want {
		t.Errorf("text stdout:\n%s\nwant:\n%s", stdout, want)
	}

	_, stdout, _ = runCmd(t, r, profiles,
		Options{Command: "outdated", Format: FormatJSON})
	doc := decodeOutdated(t, stdout)
	if len(doc.Outdated) != 1 || len(doc.Outdated[0].Installs) != 1 {
		t.Fatalf("outdated = %+v, want one install", doc.Outdated)
	}
	if in := doc.Outdated[0].Installs[0]; in.Scope != "project" ||
		in.Version != "0.9.0" {
		t.Errorf("install = %+v, want the project-scope 0.9.0", in)
	}
}

func TestOutdatedLatestFromOtherProfileCatalog(t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "1.0.0"}),
		workProfile.Path: pluginList(nil, map[string]string{"foo@acme": "1.1.0"}),
	})
	_, stdout, _ := runCmd(t, r, []config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatText})
	want := "foo@acme  latest 1.1.0\n  home  1.0.0\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestOutdatedLabelFallsBackToPath(t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		"/p/bare": pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "2.0.0"}),
	})
	_, stdout, _ := runCmd(t, r, []config.Profile{{Path: "/p/bare"}},
		Options{Command: "outdated", Format: FormatText})
	if !strings.Contains(stdout, "  /p/bare  1.0.0") {
		t.Errorf("stdout = %q, want the path as label", stdout)
	}
}

func failingWorkRunner() *claudecli.FakeRunner {
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "2.0.0"}),
	})
	r.ResponsesByDir[workProfile.Path] = map[string]claudecli.FakeResponse{
		pluginListKey: {Err: errors.New("boom")},
	}
	return r
}

func TestOutdatedProfileErrorText(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, failingWorkRunner(),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatText})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stderr != "error: work: boom\n" {
		t.Errorf("stderr = %q", stderr)
	}
	if stdout != "foo@acme  latest 2.0.0\n  home  1.0.0\n" {
		t.Errorf("stdout = %q, want the other profile still listed", stdout)
	}
}

func TestOutdatedProfileErrorJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, failingWorkRunner(),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	doc := decodeOutdated(t, stdout)
	if doc.Profiles[1].Error != "boom" || doc.Profiles[1].Refresh != "skipped" {
		t.Errorf("work profile = %+v", doc.Profiles[1])
	}
	if doc.Profiles[0].Error != "" {
		t.Errorf("home profile = %+v, want no error", doc.Profiles[0])
	}
	if len(doc.Outdated) != 1 || doc.Outdated[0].Plugin != "foo@acme" {
		t.Errorf("outdated = %+v, want foo from home", doc.Outdated)
	}
}

func TestOutdatedAllErroredPrintsNoUpToDateClaim(t *testing.T) {
	t.Parallel()
	r := &claudecli.FakeRunner{Default: claudecli.FakeResponse{
		Err: errors.New("boom"),
	}}
	code, stdout, stderr := runCmd(t, r, []config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatText})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty: nothing was checked", stdout)
	}
	if stderr != "error: home: boom\n" {
		t.Errorf("stderr = %q", stderr)
	}
}

func upToDateRunner() *claudecli.FakeRunner {
	return newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "1.0.0"}),
	})
}

func TestOutdatedEmptyText(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, upToDateRunner(),
		[]config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatText})
	if code != 0 || stdout != "all plugins up to date\n" || stderr != "" {
		t.Errorf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestOutdatedEmptyJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, upToDateRunner(),
		[]config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatJSON})
	if code != 0 || stderr != "" {
		t.Errorf("code %d stderr %q, want 0 and empty", code, stderr)
	}
	if !strings.Contains(stdout, `"outdated":[]`) {
		t.Errorf("stdout = %s, want an empty outdated array", stdout)
	}
	doc := decodeOutdated(t, stdout)
	if len(doc.Profiles) != 1 || doc.Profiles[0].Refresh != "skipped" ||
		doc.Profiles[0].Error != "" {
		t.Errorf("profiles = %+v, want home with refresh skipped", doc.Profiles)
	}
}

func countCalls(r *claudecli.FakeRunner, key string) map[string]int {
	got := map[string]int{}
	for _, c := range r.Calls {
		if strings.Join(c.Args, " ") == key {
			got[c.ProfileDir]++
		}
	}
	return got
}

func TestOutdatedWithoutRefreshSkipsUpdate(t *testing.T) {
	t.Parallel()
	r := mixedRunner()
	runCmd(t, r, []config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON})
	if got := countCalls(r, marketUpdateKy); len(got) != 0 {
		t.Errorf("marketplace update calls = %v, want none", got)
	}
}

func TestOutdatedRefreshUpdatesEachProfile(t *testing.T) {
	t.Parallel()
	r := mixedRunner()
	code, stdout, _ := runCmd(t, r, []config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON, Refresh: true})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	got := countCalls(r, marketUpdateKy)
	if got[homeProfile.Path] != 1 || got[workProfile.Path] != 1 || len(got) != 2 {
		t.Errorf("marketplace update calls = %v, want one per profile", got)
	}
	for _, p := range decodeOutdated(t, stdout).Profiles {
		if p.Refresh != "ok" {
			t.Errorf("profile %s refresh = %q, want ok", p.Label, p.Refresh)
		}
	}
}

func failingRefreshRunner() *claudecli.FakeRunner {
	r := upToDateRunner()
	r.ResponsesByDir[homeProfile.Path][marketUpdateKy] =
		claudecli.FakeResponse{Err: errors.New("offline")}
	return r
}

func TestOutdatedRefreshFailureText(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, failingRefreshRunner(),
		[]config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatText, Refresh: true})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	want := "warning: home: marketplace refresh failed; using cached catalog\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if stdout != "all plugins up to date\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestOutdatedRefreshFailureJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, failingRefreshRunner(),
		[]config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatJSON, Refresh: true})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if p := decodeOutdated(t, stdout).Profiles[0]; p.Refresh != "failed" {
		t.Errorf("refresh = %q, want failed", p.Refresh)
	}
}

func TestOutdatedAllErroredJSON(t *testing.T) {
	t.Parallel()
	r := &claudecli.FakeRunner{Default: claudecli.FakeResponse{
		Err: errors.New("boom"),
	}}
	code, stdout, stderr := runCmd(t, r,
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON})
	if code != 1 || stderr != "" {
		t.Errorf("code %d stderr %q, want 1 and empty", code, stderr)
	}
	doc := decodeOutdated(t, stdout)
	if len(doc.Profiles) != 2 {
		t.Fatalf("profiles = %+v, want both", doc.Profiles)
	}
	for _, p := range doc.Profiles {
		if p.Error != "boom" || p.Refresh != "skipped" {
			t.Errorf("profile = %+v, want error boom, refresh skipped", p)
		}
	}
	if !strings.Contains(stdout, `"outdated":[]`) {
		t.Errorf("stdout = %s, want an empty outdated array", stdout)
	}
}

// With no catalog entry there is no latest version, so nothing is outdated.
func TestOutdatedEmptyCatalog(t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "0.34.0", scope: "user", enabled: true},
		}, nil),
	})
	code, stdout, stderr := runCmd(t, r, []config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatText})
	if code != 0 || stdout != "all plugins up to date\n" || stderr != "" {
		t.Errorf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// Install rows share one column layout across every plugin group.
func TestOutdatedTextAlignsAcrossPlugins(t *testing.T) {
	t.Parallel()
	laptop := config.Profile{Path: "/p/laptop", Label: "laptop"}
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "bar@acme", version: "1.0.0", scope: "user", enabled: false},
		}, map[string]string{"bar@acme": "2.0.0"}),
		laptop.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "10.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "11.0.0"}),
	})
	_, stdout, _ := runCmd(t, r, []config.Profile{homeProfile, laptop},
		Options{Command: "outdated", Format: FormatText})
	want := `bar@acme  latest 2.0.0
  home    1.0.0   (disabled)
foo@acme  latest 11.0.0
  laptop  10.0.0
`
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

// Catalog values and labels are third-party text: control characters must
// not forge output lines or reach the terminal raw.
func TestOutdatedTextQuotesControlCharacters(t *testing.T) {
	t.Parallel()
	forged := "2.0.0\nfake@x  latest 9.9.9"
	evil := config.Profile{Path: "/p/evil", Label: "ho\x1bme"}
	r := newOutdatedRunner(map[string][]byte{
		evil.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": forged}),
	})
	r.ResponsesByDir[workProfile.Path] = map[string]claudecli.FakeResponse{
		pluginListKey: {Err: errors.New("bad\nerror: forged")},
	}
	_, stdout, stderr := runCmd(t, r, []config.Profile{evil, workProfile},
		Options{Command: "outdated", Format: FormatText})
	want := "foo@acme  latest " + strconv.Quote(forged) + "\n  " +
		strconv.Quote(evil.Label) + "  1.0.0\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	want = "error: work: " + strconv.Quote("bad\nerror: forged") + "\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// Default labels are directory names, so two profiles can share one; the
// path then tells them apart.
func TestOutdatedDuplicateLabelsFallBackToPath(t *testing.T) {
	t.Parallel()
	a := config.Profile{Path: "/a/.claude", Label: ".claude"}
	b := config.Profile{Path: "/b/.claude", Label: ".claude"}
	list := pluginList([]fakeInstall{
		{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
	}, map[string]string{"foo@acme": "2.0.0"})
	r := newOutdatedRunner(map[string][]byte{
		a.Path: list, b.Path: list, homeProfile.Path: list,
	})
	profiles := []config.Profile{a, b, homeProfile}

	_, stdout, _ := runCmd(t, r, profiles,
		Options{Command: "outdated", Format: FormatText})
	want := "foo@acme  latest 2.0.0\n" +
		"  /a/.claude  1.0.0\n  /b/.claude  1.0.0\n  home        1.0.0\n"
	if stdout != want {
		t.Errorf("text stdout = %q, want %q", stdout, want)
	}

	_, stdout, _ = runCmd(t, r, profiles,
		Options{Command: "outdated", Format: FormatJSON})
	doc := decodeOutdated(t, stdout)
	var labels []string
	for _, p := range doc.Profiles {
		labels = append(labels, p.Label)
	}
	for _, in := range doc.Outdated[0].Installs {
		labels = append(labels, in.Label)
	}
	wantLabels := "/a/.claude /b/.claude home /a/.claude /b/.claude home"
	if got := strings.Join(labels, " "); got != wantLabels {
		t.Errorf("JSON labels = %q, want %q", got, wantLabels)
	}
}

func TestOutdatedJSONLabelFallsBackToPath(t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		"/p/bare": pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "2.0.0"}),
	})
	_, stdout, _ := runCmd(t, r, []config.Profile{{Path: "/p/bare"}},
		Options{Command: "outdated", Format: FormatJSON})
	doc := decodeOutdated(t, stdout)
	if doc.Profiles[0].Label != "/p/bare" ||
		doc.Outdated[0].Installs[0].Label != "/p/bare" {
		t.Errorf("doc = %+v, want the path as label", doc)
	}
}

func TestOutdatedProfileErrorWithRefresh(t *testing.T) {
	t.Parallel()
	profiles := []config.Profile{homeProfile, workProfile}
	code, _, stderr := runCmd(t, failingWorkRunner(), profiles,
		Options{Command: "outdated", Format: FormatText, Refresh: true})
	if code != 1 || stderr != "error: work: boom\n" {
		t.Errorf("text: code %d stderr %q, want 1 and only the error", code, stderr)
	}

	code, stdout, stderr := runCmd(t, failingWorkRunner(), profiles,
		Options{Command: "outdated", Format: FormatJSON, Refresh: true})
	if code != 1 || stderr != "" {
		t.Errorf("json: code %d stderr %q, want 1 and empty", code, stderr)
	}
	doc := decodeOutdated(t, stdout)
	if p := doc.Profiles[0]; p.Refresh != "ok" || p.Error != "" {
		t.Errorf("home = %+v, want refresh ok", p)
	}
	if p := doc.Profiles[1]; p.Refresh != "skipped" || p.Error != "boom" {
		t.Errorf("work = %+v, want refresh skipped and error boom", p)
	}
}

func TestInstallMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		scope   string
		enabled bool
		want    string
	}{
		{"user", true, ""},
		{"", true, ""},
		{"user", false, "(disabled)"},
		{"project", true, "(scope: project)"},
		{"project", false, "(disabled) (scope: project)"},
	}
	for _, tt := range tests {
		p := claudecli.InstalledPlugin{Scope: tt.scope, Enabled: tt.enabled}
		if got := installMarkers(p); got != tt.want {
			t.Errorf("installMarkers(%q, %v) = %q, want %q",
				tt.scope, tt.enabled, got, tt.want)
		}
	}
}

// deadlineRunner wraps a FakeRunner and records how far away each call's
// deadline was, keyed by "<dir> <args>"; zero means no deadline.
type deadlineRunner struct {
	*claudecli.FakeRunner
	mu        sync.Mutex
	remaining map[string]time.Duration
}

func (d *deadlineRunner) Run(ctx context.Context, dir string,
	args ...string) ([]byte, error) {
	var left time.Duration
	if dl, ok := ctx.Deadline(); ok {
		left = time.Until(dl)
	}
	d.mu.Lock()
	d.remaining[dir+" "+strings.Join(args, " ")] = left
	d.mu.Unlock()
	return d.FakeRunner.Run(ctx, dir, args...)
}

func TestOutdatedBoundsEachProfileLoad(t *testing.T) {
	t.Parallel()
	r := &deadlineRunner{FakeRunner: mixedRunner(),
		remaining: map[string]time.Duration{}}
	runCmd(t, r, []config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON})
	for _, dir := range []string{homeProfile.Path, workProfile.Path} {
		if left := r.remaining[dir+" "+pluginListKey]; left <= 0 {
			t.Errorf("%s: plugin list ran without a deadline", dir)
		}
	}
}

// marketsUnknownRunner fails work's marketplace list, so work's installed
// plugin (absent from `available`) is never checked against a catalog.
func marketsUnknownRunner(homeVersion string) *claudecli.FakeRunner {
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: homeVersion, scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "2.0.0"}),
		workProfile.Path: pluginList([]fakeInstall{
			{id: "bar@acme", version: "1.0.0", scope: "user", enabled: true},
		}, nil),
	})
	r.ResponsesByDir[workProfile.Path][marketListKey] = claudecli.FakeResponse{
		Err: errors.New("list boom"),
	}
	return r
}

func TestOutdatedMarketplacesUnknownText(t *testing.T) {
	t.Parallel()
	profiles := []config.Profile{homeProfile, workProfile}
	wantErr := "error: work: marketplace list failed; " +
		"its catalogs were not read, so outdated plugins may be missed\n"

	code, stdout, stderr := runCmd(t, marketsUnknownRunner("1.0.0"), profiles,
		Options{Command: "outdated", Format: FormatText})
	if code != ExitFailure {
		t.Errorf("exit code = %d, want %d", code, ExitFailure)
	}
	if stderr != wantErr {
		t.Errorf("stderr = %q, want %q", stderr, wantErr)
	}
	if stdout != "foo@acme  latest 2.0.0\n  home  1.0.0\n" {
		t.Errorf("stdout = %q, want home's outdated plugin", stdout)
	}

	code, stdout, stderr = runCmd(t, marketsUnknownRunner("2.0.0"), profiles,
		Options{Command: "outdated", Format: FormatText})
	if code != ExitFailure {
		t.Errorf("up to date: exit code = %d, want %d", code, ExitFailure)
	}
	if stdout != "" {
		t.Errorf("up to date: stdout = %q, want no up-to-date claim", stdout)
	}
	if stderr != wantErr {
		t.Errorf("up to date: stderr = %q, want %q", stderr, wantErr)
	}
}

// An incomplete profile's installs still meet the other profiles' catalogs,
// and a version behind one of those is outdated for certain.
func TestOutdatedMarketplacesUnknownListsInstallsBehindOtherCatalogs(
	t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList(nil, map[string]string{"bar@acme": "2.0.0"}),
		workProfile.Path: pluginList([]fakeInstall{
			{id: "bar@acme", version: "1.0.0", scope: "user", enabled: true},
		}, nil),
	})
	r.ResponsesByDir[workProfile.Path][marketListKey] = claudecli.FakeResponse{
		Err: errors.New("list boom"),
	}

	code, stdout, stderr := runCmd(t, r,
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatText})
	if code != ExitFailure {
		t.Errorf("exit code = %d, want %d", code, ExitFailure)
	}
	if stdout != "bar@acme  latest 2.0.0\n  work  1.0.0\n" {
		t.Errorf("stdout = %q, want work's install behind home's catalog",
			stdout)
	}
	if !strings.Contains(stderr, "outdated plugins may be missed") {
		t.Errorf("stderr = %q, want the incomplete warning", stderr)
	}
}

func TestOutdatedMarketplacesUnknownJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, marketsUnknownRunner("1.0.0"),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "outdated", Format: FormatJSON})
	if code != ExitFailure {
		t.Errorf("exit code = %d, want %d", code, ExitFailure)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	doc := decodeOutdated(t, stdout)
	if doc.Profiles[0].Incomplete || doc.Profiles[0].Error != "" {
		t.Errorf("home profile = %+v, want complete", doc.Profiles[0])
	}
	if !doc.Profiles[1].Incomplete || doc.Profiles[1].Error != "" {
		t.Errorf("work profile = %+v, want incomplete, no error",
			doc.Profiles[1])
	}
	if len(doc.Outdated) != 1 || doc.Outdated[0].Plugin != "foo@acme" {
		t.Errorf("outdated = %+v, want foo from home", doc.Outdated)
	}
}

// A catalog version that is a commit hash must not mask a release that
// another profile's catalog reports, whichever profile comes first.
func TestOutdatedHashLatestDoesNotMaskRelease(t *testing.T) {
	t.Parallel()
	r := newOutdatedRunner(map[string][]byte{
		homeProfile.Path: pluginList([]fakeInstall{
			{id: "foo@acme", version: "1.0.0", scope: "user", enabled: true},
		}, map[string]string{"foo@acme": "1.2.0"}),
		workProfile.Path: pluginList(nil,
			map[string]string{"foo@acme": "deadbeef"}),
	})
	for _, profiles := range [][]config.Profile{
		{homeProfile, workProfile}, {workProfile, homeProfile},
	} {
		code, stdout, _ := runCmd(t, r, profiles,
			Options{Command: "outdated", Format: FormatText})
		if code != ExitOK {
			t.Errorf("exit code = %d, want %d", code, ExitOK)
		}
		if stdout != "foo@acme  latest 1.2.0\n  home  1.0.0\n" {
			t.Errorf("stdout = %q, want foo behind 1.2.0", stdout)
		}
	}
}

const (
	widgetID      = "widget@example-market"
	installedSHA  = "1a2b3c4"
	otherSHA      = "9a8b7c6"
	latestSHA     = "5d6e7f8"
	widgetRepo    = "acme/widgets"
	widgetHistory = "https://github.com/acme/widgets/commits/5d6e7f8/plugins/widget"
)

func widgetSource() claudecli.PluginSource {
	return claudecli.PluginSource{
		RepoURL: widgetRepo, Commit: latestSHA, Path: "plugins/widget",
	}
}

// widgetLoad is a loaded profile holding one widget install. A non-empty
// latest makes the profile's catalog supply it from src.
func widgetLoad(p config.Profile, version, sha, latest string,
	src claudecli.PluginSource) profileLoad {
	id := claudecli.PluginID{Name: "widget", Marketplace: "example-market"}
	lv := claudecli.LatestVersions{
		Versions: map[claudecli.PluginID]string{id: latest},
		Sources:  map[claudecli.PluginID]claudecli.PluginSource{},
	}
	if latest != "" {
		lv.Sources[id] = src
	}
	return profileLoad{
		profile: p,
		data: claudecli.PluginData{Installed: []claudecli.InstalledPlugin{{
			ID: id, Version: version, Scope: "user", Enabled: true,
			CommitSHA: sha,
		}}},
		latest: lv,
	}
}

func renderOutdatedText(t *testing.T, loads []profileLoad) string {
	t.Helper()
	profiles := make([]config.Profile, 0, len(loads))
	for _, l := range loads {
		profiles = append(profiles, l.profile)
	}
	var out, errOut bytes.Buffer
	err := writeOutdatedText(&out, &errOut, profileLabels(profiles), loads,
		findOutdated(loads), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
	return out.String()
}

func renderOutdatedJSON(t *testing.T, loads []profileLoad) string {
	t.Helper()
	profiles := make([]config.Profile, 0, len(loads))
	for _, l := range loads {
		profiles = append(profiles, l.profile)
	}
	var out bytes.Buffer
	err := writeOutdatedJSON(&out, profileLabels(profiles), loads,
		findOutdated(loads), false)
	if err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestOutdatedTextChangeLinks(t *testing.T) {
	t.Parallel()
	loads := []profileLoad{
		widgetLoad(homeProfile, "1.2.0", installedSHA, "1.4.0", widgetSource()),
		// No recorded install commit: no compare link for this install.
		widgetLoad(workProfile, "1.3.1", "", "", claudecli.PluginSource{}),
	}
	want := `widget@example-market  latest 1.4.0
  home  1.2.0
    changes: https://github.com/acme/widgets/compare/1a2b3c4...5d6e7f8
  work  1.3.1
  history: ` + widgetHistory + "\n"
	if got := renderOutdatedText(t, loads); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

// The link must follow the profile whose catalog supplied the merged latest,
// so the compare target matches the version shown.
func TestOutdatedLinksUseSourceOfMergedLatest(t *testing.T) {
	t.Parallel()
	older := widgetSource()
	older.Commit = otherSHA
	loads := []profileLoad{
		widgetLoad(homeProfile, "1.2.0", installedSHA, "1.3.0", older),
		widgetLoad(workProfile, "1.2.0", installedSHA, "1.4.0", widgetSource()),
	}
	got := renderOutdatedText(t, loads)
	if !strings.Contains(got, "compare/1a2b3c4...5d6e7f8\n") ||
		strings.Contains(got, otherSHA) {
		t.Errorf("stdout uses the wrong source:\n%s", got)
	}
}

func TestOutdatedTextOmitsUnknownLinks(t *testing.T) {
	t.Parallel()
	noCommit := widgetSource()
	noCommit.Commit = ""
	atRoot := widgetSource()
	atRoot.Path = ""
	tests := []struct {
		name string
		src  claudecli.PluginSource
		want string
	}{
		{"no latest commit", noCommit, "  home  1.2.0\n"},
		{"root plugin", atRoot, "  home  1.2.0\n" +
			"    changes: https://github.com/acme/widgets/compare/" +
			"1a2b3c4...5d6e7f8\n"},
		{"not github", claudecli.PluginSource{
			RepoURL: "https://example.com/acme/widgets.git",
			Commit:  latestSHA, Path: "plugins/widget",
		}, "  home  1.2.0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renderOutdatedText(t, []profileLoad{
				widgetLoad(homeProfile, "1.2.0", installedSHA, "1.4.0", tt.src),
			})
			want := "widget@example-market  latest 1.4.0\n" + tt.want
			if got != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// The source path is catalog data: a control character must not reach the
// terminal raw.
func TestOutdatedTextLinksQuoteControlCharacters(t *testing.T) {
	t.Parallel()
	src := widgetSource()
	src.Path = "plugins/wid\x1bget"
	got := renderOutdatedText(t, []profileLoad{
		widgetLoad(homeProfile, "1.2.0", installedSHA, "1.4.0", src),
	})
	if strings.ContainsFunc(got, func(r rune) bool {
		return r != '\n' && r < 0x20
	}) {
		t.Errorf("stdout carries a raw control character: %q", got)
	}
	if !strings.Contains(got, "  history: https://github.com/acme/widgets/"+
		"commits/5d6e7f8/plugins/wid%1Bget\n") {
		t.Errorf("stdout = %q, want an escaped history path", got)
	}
}

// PathEscape already encodes control characters in built links; the text
// writer still quotes them in case a link ever arrives unescaped.
func TestOutdatedTextQuotesControlCharactersInLinks(t *testing.T) {
	t.Parallel()
	id := claudecli.PluginID{Name: "widget", Marketplace: "example-market"}
	outdated := []outdatedPlugin{{
		id: id, latest: "1.4.0",
		installs: []outdatedInstall{{
			profile: homeProfile,
			plugin: claudecli.InstalledPlugin{ID: id, Version: "1.2.0",
				Enabled: true, Scope: "user"},
			compare: "https://github.com/acme/w\x1bidgets/compare/a...b",
		}},
		history: "https://github.com/acme/w\x1bidgets/commits/b",
	}}
	var out, errOut bytes.Buffer
	err := writeOutdatedText(&out, &errOut,
		profileLabels([]config.Profile{homeProfile}), nil, outdated,
		false, false)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("stdout carries a raw escape: %q", got)
	}
	for _, want := range []string{
		`    changes: "https://github.com/acme/w\x1bidgets/compare/a...b"`,
		`  history: "https://github.com/acme/w\x1bidgets/commits/b"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout = %q, want quoted %s", got, want)
		}
	}
}

func TestOutdatedJSONChangeLinks(t *testing.T) {
	t.Parallel()
	loads := []profileLoad{
		widgetLoad(homeProfile, "1.2.0", installedSHA, "1.4.0", widgetSource()),
		widgetLoad(workProfile, "1.3.1", "", "", claudecli.PluginSource{}),
	}
	var doc struct {
		Outdated []map[string]json.RawMessage `json:"outdated"`
	}
	stdout := renderOutdatedJSON(t, loads)
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(doc.Outdated) != 1 {
		t.Fatalf("outdated = %s, want one plugin", stdout)
	}
	o := doc.Outdated[0]
	if got := string(o["history_url"]); got != strconv.Quote(widgetHistory) {
		t.Errorf("history_url = %s, want %q", got, widgetHistory)
	}
	var installs []map[string]json.RawMessage
	if err := json.Unmarshal(o["installs"], &installs); err != nil {
		t.Fatal(err)
	}
	wantCompare := []string{
		`"https://github.com/acme/widgets/compare/1a2b3c4...5d6e7f8"`, `""`,
	}
	if len(installs) != len(wantCompare) {
		t.Fatalf("installs = %s", o["installs"])
	}
	for i, in := range installs {
		if got := string(in["compare_url"]); got != wantCompare[i] {
			t.Errorf("install %d compare_url = %s, want %s",
				i, got, wantCompare[i])
		}
	}

	// Existing fields keep their shape.
	typed := decodeOutdated(t, stdout)
	if in := typed.Outdated[0].Installs[1]; in.Label != "work" ||
		in.Path != "/p/work" || in.Version != "1.3.1" || in.Scope != "user" ||
		!in.Enabled || typed.Outdated[0].Latest != "1.4.0" ||
		typed.Outdated[0].Plugin != widgetID {
		t.Errorf("outdated = %+v", typed.Outdated)
	}
}

func TestOutdatedJSONUnknownHistoryIsEmptyString(t *testing.T) {
	t.Parallel()
	stdout := renderOutdatedJSON(t, []profileLoad{
		widgetLoad(homeProfile, "1.2.0", "", "1.4.0", claudecli.PluginSource{}),
	})
	if !strings.Contains(stdout, `"history_url":""`) ||
		!strings.Contains(stdout, `"compare_url":""`) {
		t.Errorf("stdout = %s, want empty link strings", stdout)
	}
}

// An incomplete profile has no catalog of its own, so its install's link
// must come from the profile whose catalog supplied the latest version.
func TestOutdatedJSONIncompleteProfileGetsLinkFromOtherCatalog(
	t *testing.T) {
	t.Parallel()
	incomplete := widgetLoad(workProfile, "1.2.0", otherSHA, "",
		claudecli.PluginSource{})
	incomplete.data.MarketplacesUnknown = true
	stdout := renderOutdatedJSON(t, []profileLoad{
		widgetLoad(homeProfile, "1.4.0", latestSHA, "1.4.0", widgetSource()),
		incomplete,
	})
	if !strings.Contains(stdout, `"incomplete":true`) ||
		!strings.Contains(stdout, `"compare_url":"https://github.com/`+
			`acme/widgets/compare/9a8b7c6...5d6e7f8"`) {
		t.Errorf("stdout = %s, want an incomplete profile with a link", stdout)
	}
}

// Through Run the install commit comes from the profile's
// installed_plugins.json and the latest commit from the catalog source.
func TestOutdatedRunReadsInstalledCommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	records := `{"version":2,"plugins":{"` + widgetID +
		`":[{"scope":"user","gitCommitSha":"` + installedSHA + `"}]}}`
	err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"),
		[]byte(records), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	list := `{"installed":[{"id":"` + widgetID + `","version":"1.2.0",` +
		`"scope":"user","enabled":true}],"available":[{"pluginId":"` +
		widgetID + `","version":"1.4.0","source":{"source":"github",` +
		`"repo":"` + widgetRepo + `","sha":"` + latestSHA +
		`","path":"plugins/widget"}}]}`
	r := newOutdatedRunner(map[string][]byte{dir: []byte(list)})
	profile := config.Profile{Path: dir, Label: "home"}
	_, stdout, stderr := runCmd(t, r, []config.Profile{profile},
		Options{Command: "outdated", Format: FormatText})
	want := `widget@example-market  latest 1.4.0
  home  1.2.0
    changes: https://github.com/acme/widgets/compare/1a2b3c4...5d6e7f8
  history: ` + widgetHistory + "\n"
	if stdout != want || stderr != "" {
		t.Errorf("stdout:\n%s\nwant:\n%s\nstderr: %q", stdout, want, stderr)
	}
}

const widgetChangelog = `# Changelog

## v1.4.0 - 2026-01-01
- Add gadgets

## 1.3.1
- Fix a crash

## 1.3.0
- Speed up

## 1.2.0
- Initial release
`

// changelogSource is widgetSource backed by a clone dir whose plugin
// directory holds changelog; an empty changelog writes no file.
func changelogSource(t *testing.T, changelog string) claudecli.PluginSource {
	t.Helper()
	src := widgetSource()
	src.CloneDir = t.TempDir()
	if changelog == "" {
		return src
	}
	dir := filepath.Join(src.CloneDir, "plugins", "widget")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"),
		[]byte(changelog), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func outdatedWithChangelogs(loads []profileLoad) []outdatedPlugin {
	outdated := findOutdated(loads)
	attachChangelogs(outdated)
	return outdated
}

func renderChangelogText(t *testing.T, loads []profileLoad) string {
	t.Helper()
	var out, errOut bytes.Buffer
	err := writeOutdatedText(&out, &errOut, profileLabels(loadProfilesOf(loads)),
		loads, outdatedWithChangelogs(loads), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
	return out.String()
}

func loadProfilesOf(loads []profileLoad) []config.Profile {
	profiles := make([]config.Profile, 0, len(loads))
	for _, l := range loads {
		profiles = append(profiles, l.profile)
	}
	return profiles
}

// The excerpt starts after the oldest install, so every install sees the
// entries it is missing.
func TestOutdatedTextChangelog(t *testing.T) {
	t.Parallel()
	src := changelogSource(t, widgetChangelog)
	loads := []profileLoad{
		widgetLoad(homeProfile, "1.3.1", "", "1.4.0", src),
		widgetLoad(workProfile, "1.2.0", "", "", claudecli.PluginSource{}),
	}
	want := `widget@example-market  latest 1.4.0
  home  1.3.1
  work  1.2.0
  history: ` + widgetHistory + `
  changelog (plugins/widget/CHANGELOG.md):
    ## v1.4.0 - 2026-01-01
    - Add gadgets

    ## 1.3.1
    - Fix a crash

    ## 1.3.0
    - Speed up
`
	if got := renderChangelogText(t, loads); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

func TestOutdatedTextChangelogNothingToShow(t *testing.T) {
	t.Parallel()
	remote := widgetSource()
	tests := []struct {
		name string
		src  claudecli.PluginSource
		want string
	}{
		{"no entry for latest",
			changelogSource(t, "## 1.3.0\n- Speed up\n"),
			"  changelog: no entry for 1.4.0\n"},
		{"no changelog file", changelogSource(t, ""),
			"  changelog: no CHANGELOG.md\n"},
		{"remote source", remote, "  changelog: no local changelog\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renderChangelogText(t, []profileLoad{
				widgetLoad(homeProfile, "1.2.0", "", "1.4.0", tt.src),
			})
			want := "widget@example-market  latest 1.4.0\n  home  1.2.0\n" +
				"  history: " + widgetHistory + "\n" + tt.want
			if got != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A relative source whose clone could not be located (its profile's
// marketplace list failed) reads like a remote one: nothing local to read.
func TestOutdatedTextChangelogCloneNotLocated(t *testing.T) {
	t.Parallel()
	got := renderChangelogText(t, []profileLoad{
		widgetLoad(homeProfile, "1.2.0", "", "1.4.0",
			claudecli.PluginSource{Path: "./plugins/widget"}),
	})
	want := "widget@example-market  latest 1.4.0\n  home  1.2.0\n" +
		"  changelog: no local changelog\n"
	if got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

// Links come from a linkable remote source, but the changelog is read from
// another profile's clone of the same latest version.
func TestOutdatedTextChangelogPrefersLocalClone(t *testing.T) {
	t.Parallel()
	local := changelogSource(t, widgetChangelog)
	local.RepoURL, local.Commit = "", ""
	got := renderChangelogText(t, []profileLoad{
		widgetLoad(homeProfile, "1.3.1", "", "1.4.0", widgetSource()),
		widgetLoad(workProfile, "1.3.1", "", "1.4.0", local),
	})
	want := `widget@example-market  latest 1.4.0
  home  1.3.1
  work  1.3.1
  history: ` + widgetHistory + `
  changelog (plugins/widget/CHANGELOG.md):
    ## v1.4.0 - 2026-01-01
    - Add gadgets
`
	if got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

// Changelog text is third-party: control characters must not reach the
// terminal raw, while a tab is expanded rather than quoting the line.
func TestOutdatedTextChangelogQuotesControlCharacters(t *testing.T) {
	t.Parallel()
	src := changelogSource(t,
		"## 1.4.0\n- Add \x1b[31mred\x1b[0m\n-\tTabbed\n## 1.2.0\n")
	got := renderChangelogText(t, []profileLoad{
		widgetLoad(homeProfile, "1.2.0", "", "1.4.0", src),
	})
	if strings.ContainsFunc(got, func(r rune) bool {
		return r != '\n' && r < 0x20
	}) {
		t.Errorf("stdout carries a raw control character: %q", got)
	}
	for _, want := range []string{
		`    "- Add \x1b[31mred\x1b[0m"` + "\n",
		"    -    Tabbed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout = %q, want line %q", got, want)
		}
	}
}

func renderChangelogJSON(t *testing.T, loads []profileLoad,
	outdated []outdatedPlugin) []map[string]json.RawMessage {
	t.Helper()
	var out bytes.Buffer
	err := writeOutdatedJSON(&out, profileLabels(loadProfilesOf(loads)), loads,
		outdated, false)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Outdated []map[string]json.RawMessage `json:"outdated"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if len(doc.Outdated) != 1 {
		t.Fatalf("outdated = %s, want one plugin", out.String())
	}
	return doc.Outdated
}

func TestOutdatedJSONChangelog(t *testing.T) {
	t.Parallel()
	loads := []profileLoad{
		widgetLoad(homeProfile, "1.3.1", "", "1.4.0",
			changelogSource(t, widgetChangelog)),
		widgetLoad(workProfile, "1.2.0", "", "", claudecli.PluginSource{}),
	}
	o := renderChangelogJSON(t, loads, outdatedWithChangelogs(loads))[0]
	var got struct {
		File  string `json:"file"`
		Since string `json:"since"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(o["changelog"], &got); err != nil {
		t.Fatalf("changelog = %s: %v", o["changelog"], err)
	}
	wantText := "## v1.4.0 - 2026-01-01\n- Add gadgets\n\n## 1.3.1\n" +
		"- Fix a crash\n\n## 1.3.0\n- Speed up\n"
	if got.File != "plugins/widget/CHANGELOG.md" || got.Since != "1.2.0" ||
		got.Text != wantText {
		t.Errorf("changelog = %+v", got)
	}
}

func TestOutdatedJSONChangelogNullWhenNoneFound(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]claudecli.PluginSource{
		"no entry": changelogSource(t, "## 1.3.0\n"),
		"no file":  changelogSource(t, ""),
		"no clone": widgetSource(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			loads := []profileLoad{
				widgetLoad(homeProfile, "1.2.0", "", "1.4.0", src),
			}
			o := renderChangelogJSON(t, loads, outdatedWithChangelogs(loads))[0]
			if got, ok := o["changelog"]; !ok || string(got) != "null" {
				t.Errorf("changelog = %s (present %v), want null", got, ok)
			}
		})
	}
}

func TestOutdatedJSONChangelogAbsentWithoutFlag(t *testing.T) {
	t.Parallel()
	loads := []profileLoad{
		widgetLoad(homeProfile, "1.2.0", "", "1.4.0",
			changelogSource(t, widgetChangelog)),
	}
	o := renderChangelogJSON(t, loads, findOutdated(loads))[0]
	if got, ok := o["changelog"]; ok {
		t.Errorf("changelog = %s, want the key absent", got)
	}
}

// Through Run a remote catalog source has no clone, and a missing
// changelog is informational: the exit code stays 0.
func TestOutdatedRunChangelogKeepsExitCode(t *testing.T) {
	t.Parallel()
	list := `{"installed":[{"id":"` + widgetID + `","version":"1.2.0",` +
		`"scope":"user","enabled":true}],"available":[{"pluginId":"` +
		widgetID + `","version":"1.4.0","source":{"source":"github",` +
		`"repo":"` + widgetRepo + `","sha":"` + latestSHA +
		`","path":"plugins/widget"}}]}`
	r := newOutdatedRunner(map[string][]byte{homeProfile.Path: []byte(list)})
	code, stdout, stderr := runCmd(t, r, []config.Profile{homeProfile},
		Options{Command: "outdated", Format: FormatText, Changelog: true})
	if code != ExitOK || stderr != "" ||
		!strings.HasSuffix(stdout, "  changelog: no local changelog\n") {
		t.Errorf("code = %d, stderr = %q, stdout:\n%s", code, stderr, stdout)
	}
}
