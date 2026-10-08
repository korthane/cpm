package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/korthane/cpm/internal/claudecli"
	"github.com/korthane/cpm/internal/config"
)

type refreshResult struct {
	profile config.Profile
	err     error
}

func runRefresh(ctx context.Context, r claudecli.Runner,
	profiles []config.Profile, opts Options, stdout, stderr io.Writer) int {
	results := mapProfiles(ctx, profiles,
		func(ctx context.Context, p config.Profile) refreshResult {
			return refreshResult{
				profile: p,
				err:     claudecli.RefreshMarketplaces(ctx, r, p.Path),
			}
		})

	failed := slices.ContainsFunc(results,
		func(res refreshResult) bool { return res.err != nil })
	labels := profileLabels(profiles)
	var err error
	if opts.Format == FormatJSON {
		err = writeRefreshJSON(stdout, labels, results)
	} else {
		err = writeRefreshText(stdout, stderr, labels, results)
	}
	return ExitCode(failed, err, stderr)
}

// writeRefreshText returns the stdout write error; stderr diagnostics are
// best-effort.
func writeRefreshText(stdout, stderr io.Writer, labels map[string]string,
	results []refreshResult) error {
	// Render into memory so one Write reports any stdout failure.
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	for _, res := range results {
		label := quoteControl(labels[res.profile.Path])
		if res.err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %s: %s\n", label,
				quoteControl(res.err.Error()))
			continue
		}
		_, _ = fmt.Fprintf(tw, "%s\tok\n", label)
	}
	_ = tw.Flush()
	if buf.Len() == 0 {
		return nil
	}
	_, err := stdout.Write(buf.Bytes())
	return err
}

type refreshJSON struct {
	Profiles []refreshProfileJSON `json:"profiles"`
}

type refreshProfileJSON struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

func writeRefreshJSON(stdout io.Writer, labels map[string]string,
	results []refreshResult) error {
	// Non-nil slice: the documented shape promises an array, never null.
	doc := refreshJSON{Profiles: make([]refreshProfileJSON, 0, len(results))}
	for _, res := range results {
		p := refreshProfileJSON{
			Label: labels[res.profile.Path],
			Path:  res.profile.Path,
		}
		if res.err != nil {
			p.Error = res.err.Error()
		}
		doc.Profiles = append(doc.Profiles, p)
	}
	return json.NewEncoder(stdout).Encode(doc)
}
