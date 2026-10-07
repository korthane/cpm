package cli

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

type jsonRefresh struct {
	Profiles []struct {
		Label string `json:"label"`
		Path  string `json:"path"`
		Error string `json:"error"`
	} `json:"profiles"`
}

func decodeRefresh(t *testing.T, stdout string) jsonRefresh {
	t.Helper()
	var doc jsonRefresh
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	return doc
}

// refreshRunner fails `marketplace update` for the given profile dirs.
func refreshRunner(failing ...string) *claudecli.FakeRunner {
	r := &claudecli.FakeRunner{
		ResponsesByDir: map[string]map[string]claudecli.FakeResponse{},
	}
	for _, dir := range failing {
		r.ResponsesByDir[dir] = map[string]claudecli.FakeResponse{
			marketUpdateKy: {Err: errors.New("offline")},
		}
	}
	return r
}

func TestRefreshUpdatesEachProfile(t *testing.T) {
	t.Parallel()
	r := refreshRunner()
	code, stdout, stderr := runCmd(t, r,
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "refresh", Format: FormatText})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	got := countCalls(r, marketUpdateKy)
	if got[homeProfile.Path] != 1 || got[workProfile.Path] != 1 || len(got) != 2 {
		t.Errorf("marketplace update calls = %v, want one per profile", got)
	}
	if len(r.Calls) != 2 {
		t.Errorf("calls = %v, want only the two updates", r.Calls)
	}
	if stdout != "home  ok\nwork  ok\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestRefreshLabelFallsBackToPath(t *testing.T) {
	t.Parallel()
	_, stdout, _ := runCmd(t, refreshRunner(),
		[]config.Profile{{Path: "/p/bare"}},
		Options{Command: "refresh", Format: FormatText})
	if stdout != "/p/bare  ok\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRefreshFailureText(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, refreshRunner(workProfile.Path),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "refresh", Format: FormatText})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout != "home  ok\n" {
		t.Errorf("stdout = %q, want only the refreshed profile", stdout)
	}
	if stderr != "error: work: offline\n" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRefreshJSON(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCmd(t, refreshRunner(workProfile.Path),
		[]config.Profile{homeProfile, workProfile},
		Options{Command: "refresh", Format: FormatJSON})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty in JSON mode", stderr)
	}
	doc := decodeRefresh(t, stdout)
	if len(doc.Profiles) != 2 {
		t.Fatalf("profiles = %+v, want 2", doc.Profiles)
	}
	home, work := doc.Profiles[0], doc.Profiles[1]
	if home.Label != "home" || home.Path != homeProfile.Path || home.Error != "" {
		t.Errorf("home = %+v", home)
	}
	if work.Label != "work" || work.Path != workProfile.Path ||
		work.Error != "offline" {
		t.Errorf("work = %+v", work)
	}
}

func TestRefreshJSONNoProfilesIsEmptyArray(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runCmd(t, refreshRunner(), nil,
		Options{Command: "refresh", Format: FormatJSON})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "{\"profiles\":[]}\n" {
		t.Errorf("stdout = %q", stdout)
	}
}
