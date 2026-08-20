package agent

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// errStubWriteFailed stands in for a failed credentials persist.
var errStubWriteFailed = errors.New("stub: write failed")

// newCredsWriterAgent builds the minimum agent needed to exercise
// applyRefreshedTokens: a client whose token can be inspected and a logger.
func newCredsWriterAgent(t *testing.T) *AnthropicAgent {
	t.Helper()
	// Isolate any ambient-writer fallback from the developer's real session.
	t.Setenv("HOME", t.TempDir())
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	return &AnthropicAgent{
		client: api.NewAnthropicClient("stale-access-token", logger),
		logger: logger,
	}
}

// TestApplyRefreshedTokens_UsesAccountWriter pins the multi-account contract:
// a named account rotates its own credentials file, never the ambient one.
//
// This is a merge guard. Upstream's applyRefreshedTokens (issue #111) calls
// api.WriteAnthropicCredentials directly, which resolves to $HOME/.claude/
// .credentials.json. With ANTHROPIC_AUTH_ROOT set - as the with-user-env image
// does - every account is a named account, so that ambient path is one nothing
// reads: the refresh token would be burned server-side while the file the
// agent polls from never changes, which is unrecoverable.
func TestApplyRefreshedTokens_UsesAccountWriter(t *testing.T) {
	a := newCredsWriterAgent(t)

	var gotAccess, gotRefresh string
	var gotExpiresIn int
	var calls int
	a.SetCredentialsWriter(func(accessToken, refreshToken string, expiresIn int) error {
		calls++
		gotAccess, gotRefresh, gotExpiresIn = accessToken, refreshToken, expiresIn
		return nil
	})

	a.applyRefreshedTokens(&api.OAuthTokenResponse{
		AccessToken:  "fresh-access-token",
		RefreshToken: "fresh-refresh-token",
		ExpiresIn:    28800,
	}, "stale-access-token")

	if calls != 1 {
		t.Fatalf("account writer calls = %d, want 1", calls)
	}
	if gotAccess != "fresh-access-token" || gotRefresh != "fresh-refresh-token" {
		t.Fatalf("writer got (%q, %q), want (fresh-access-token, fresh-refresh-token)", gotAccess, gotRefresh)
	}
	if gotExpiresIn != 28800 {
		t.Fatalf("writer got expiresIn = %d, want 28800", gotExpiresIn)
	}
	if a.lastToken != "fresh-access-token" {
		t.Fatalf("lastToken = %q, want fresh-access-token", a.lastToken)
	}
	// A successful write means the stored credentials are current, so nothing
	// is superseded and the pre-poll re-read may trust the file.
	if a.supersededToken != "" {
		t.Fatalf("supersededToken = %q, want empty after a successful write", a.supersededToken)
	}
}

// TestApplyRefreshedTokens_WriterFailureMarksSuperseded covers the issue #111
// half of the contract on the account-writer path: the refresh token is already
// consumed server-side, so the new access token must survive in memory and the
// on-disk token must be marked stale so the pre-poll re-read cannot downgrade
// back to it.
func TestApplyRefreshedTokens_WriterFailureMarksSuperseded(t *testing.T) {
	a := newCredsWriterAgent(t)
	a.SetCredentialsWriter(func(string, string, int) error {
		return errStubWriteFailed
	})

	a.applyRefreshedTokens(&api.OAuthTokenResponse{
		AccessToken:  "fresh-access-token",
		RefreshToken: "fresh-refresh-token",
		ExpiresIn:    28800,
	}, "stale-access-token")

	if a.lastToken != "fresh-access-token" {
		t.Fatalf("lastToken = %q, want fresh-access-token retained in memory", a.lastToken)
	}
	if a.supersededToken != "stale-access-token" {
		t.Fatalf("supersededToken = %q, want stale-access-token", a.supersededToken)
	}
}

// TestApplyRefreshedTokens_FallsBackToAmbientWriter keeps the ambient (single
// account) path working when no per-account writer is installed. The ambient
// writer requires an existing credentials file, so the only thing asserted here
// is that the token still lands in memory - the fallback is exercised, not the
// filesystem layout.
func TestApplyRefreshedTokens_FallsBackToAmbientWriter(t *testing.T) {
	a := newCredsWriterAgent(t)
	if a.credsWrite != nil {
		t.Fatal("credsWrite should be nil without SetCredentialsWriter")
	}

	a.applyRefreshedTokens(&api.OAuthTokenResponse{
		AccessToken:  "fresh-access-token",
		RefreshToken: "fresh-refresh-token",
		ExpiresIn:    28800,
	}, "stale-access-token")

	if a.lastToken != "fresh-access-token" {
		t.Fatalf("lastToken = %q, want fresh-access-token", a.lastToken)
	}
}
