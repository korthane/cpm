package claudecli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestLoadAuthStatus(t *testing.T) {
	tests := []struct {
		name    string
		stdout  []byte
		runErr  error
		want    AuthStatus
		wantErr bool
	}{
		{
			name:   "logged in fixture",
			stdout: readFixture(t, "auth_status.json"),
			want: AuthStatus{
				LoggedIn:         true,
				Email:            "user@example.com",
				SubscriptionType: "max",
			},
		},
		{
			name:   "logged out fixture degrades to blanks",
			stdout: readFixture(t, "auth_status_logged_out.json"),
			want:   AuthStatus{LoggedIn: false},
		},
		{
			name:   "missing fields degrade to blanks",
			stdout: []byte(`{"loggedIn": true}`),
			want:   AuthStatus{LoggedIn: true},
		},
		{
			name:    "malformed JSON is an error",
			stdout:  []byte(`not json at all`),
			wantErr: true,
		},
		{
			name:    "empty output is an error",
			stdout:  nil,
			wantErr: true,
		},
		{
			name:    "runner failure without output is an error",
			runErr:  errors.New("spawn failed"),
			wantErr: true,
		},
		{
			// Logged-out invocations may exit non-zero while still printing
			// valid JSON; the parsed status must win over the exit status.
			name:   "valid JSON wins over runner error",
			stdout: []byte(`{"loggedIn": false}`),
			runErr: errors.New("exit status 1"),
			want:   AuthStatus{LoggedIn: false},
		},
		{
			// `null` unmarshals into the zero value without error; it must
			// not mask a failed invocation as a logged-out status.
			name:    "null stdout does not mask runner error",
			stdout:  []byte("null"),
			runErr:  errors.New("exit status 1"),
			wantErr: true,
		},
		{
			name:    "null stdout without runner error is an error",
			stdout:  []byte("null"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FakeRunner{
				Responses: map[string]FakeResponse{
					"auth status --json": {Stdout: tt.stdout, Err: tt.runErr},
				},
			}

			got, err := LoadAuthStatus(t.Context(), f, "/home/u/.claude")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadAuthStatus = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadAuthStatusInvokesCorrectCommand(t *testing.T) {
	f := &FakeRunner{Default: FakeResponse{Stdout: []byte(`{"loggedIn": true}`)}}

	if _, err := LoadAuthStatus(t.Context(), f, "/tmp/profile-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.Calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(f.Calls))
	}
	call := f.Calls[0]
	if call.ProfileDir != "/tmp/profile-x" {
		t.Errorf("ProfileDir = %q, want %q", call.ProfileDir, "/tmp/profile-x")
	}
	if strings.Join(call.Args, " ") != "auth status --json" {
		t.Errorf("Args = %v, want [auth status --json]", call.Args)
	}
}

func TestLoadAuthStatusPropagatesRunError(t *testing.T) {
	wantErr := &RunError{Args: []string{"auth", "status", "--json"}, ExitCode: 1, Err: errors.New("exit status 1")}
	f := &FakeRunner{Default: FakeResponse{Err: wantErr}}

	_, err := LoadAuthStatus(t.Context(), f, "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
