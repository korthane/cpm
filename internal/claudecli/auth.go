package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// AuthStatus describes the account signed into a profile, as reported by
// `claude auth status --json`. Fields absent from the output (e.g. for a
// logged-out profile) stay at their zero values.
type AuthStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
}

// LoadAuthStatus fetches and parses the auth status for one profile.
func LoadAuthStatus(ctx context.Context, r Runner, profileDir string) (AuthStatus, error) {
	out, runErr := r.Run(ctx, profileDir, "auth", "status", "--json")

	// A logged-out profile may exit non-zero while still printing valid
	// JSON, so a parseable JSON object takes precedence over the exit
	// status. Requiring an object guards against stdout like `null`, which
	// unmarshals into the zero value without error and would mask a real
	// failure as a logged-out status.
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var status AuthStatus
		parseErr := json.Unmarshal(trimmed, &status)
		if parseErr == nil {
			return status, nil
		}
		if runErr == nil {
			return AuthStatus{}, fmt.Errorf("parse auth status: %w", parseErr)
		}
	}
	if runErr != nil {
		return AuthStatus{}, runErr
	}
	return AuthStatus{}, fmt.Errorf("parse auth status: unexpected output %q", trimmed)
}
