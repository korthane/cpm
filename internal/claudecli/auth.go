package claudecli

import (
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
	// JSON, so parseable stdout takes precedence over the exit status.
	var status AuthStatus
	parseErr := json.Unmarshal(out, &status)
	if parseErr == nil {
		return status, nil
	}
	if runErr != nil {
		return AuthStatus{}, runErr
	}
	return AuthStatus{}, fmt.Errorf("parse auth status: %w", parseErr)
}
