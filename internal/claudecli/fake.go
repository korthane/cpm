package claudecli

import (
	"context"
	"slices"
	"strings"
)

// FakeRunner is a test double for Runner. It returns canned responses keyed by
// the space-joined args and records every invocation. It lives outside a
// _test.go file so tests in other packages (config, ui) can inject it too.
// Not safe for concurrent use: tests drive Model.Update directly, so commands
// run sequentially; driving a real tea.Program with it (batched commands run
// in parallel goroutines) would race on Calls.
type FakeRunner struct {
	// Responses maps a space-joined args string to the canned result.
	Responses map[string]FakeResponse
	// ResponsesByDir maps a profile dir to args-keyed responses consulted
	// before Responses, so a test can vary one command's answer per profile
	// (e.g. the default-profile auth fallback re-runs `auth status` with an
	// empty dir and must see a different result).
	ResponsesByDir map[string]map[string]FakeResponse
	// Default is returned when no key in Responses matches; its zero value
	// yields empty output and a nil error.
	Default FakeResponse
	// Calls records every invocation in order.
	Calls []FakeCall
}

// FakeResponse is the canned stdout/error for a matched invocation.
type FakeResponse struct {
	Stdout []byte
	Err    error
}

// FakeCall records the arguments of a single Run invocation.
type FakeCall struct {
	ProfileDir string
	Args       []string
}

// Run records the call and returns the matching canned response, or Default.
func (f *FakeRunner) Run(_ context.Context, profileDir string, args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, FakeCall{ProfileDir: profileDir, Args: slices.Clone(args)})
	key := strings.Join(args, " ")
	if resp, ok := f.ResponsesByDir[profileDir][key]; ok {
		return resp.Stdout, resp.Err
	}
	if resp, ok := f.Responses[key]; ok {
		return resp.Stdout, resp.Err
	}
	return f.Default.Stdout, f.Default.Err
}
