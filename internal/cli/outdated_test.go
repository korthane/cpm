package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
