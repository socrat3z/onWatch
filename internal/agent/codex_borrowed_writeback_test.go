package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// authFileFields is the raw shape written to auth.json, including fields the
// write-back must preserve.
type authFileFields struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
	UnknownExtra string `json:"unknown_extra,omitempty"`
	Tokens       struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

// writeAuthFile writes a Codex-format auth.json under the sandboxed HOME and
// returns its path.
func writeAuthFile(t *testing.T, fields authFileFields) string {
	t.Helper()

	authDir := filepath.Join(testHomeDir(t), ".codex")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal auth.json: %v", err)
	}
	path := filepath.Join(authDir, "auth.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	return path
}

// writeAuthJSON is the common case: just tokens.
func writeAuthJSON(t *testing.T, accessToken, refreshToken, idToken, accountID string) string {
	t.Helper()
	var f authFileFields
	f.Tokens.AccessToken = accessToken
	f.Tokens.RefreshToken = refreshToken
	f.Tokens.IDToken = idToken
	f.Tokens.AccountID = accountID
	return writeAuthFile(t, f)
}

func readAuthFile(t *testing.T, path string) authFileFields {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var f authFileFields
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal auth.json: %v", err)
	}
	return f
}

func readAuthRefreshToken(t *testing.T, path string) string {
	t.Helper()
	return readAuthFile(t, path).Tokens.RefreshToken
}

func readProfile(t *testing.T, path string) CodexProfile {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var profile CodexProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	return profile
}

// codexSharedAuthFixture drives a named profile agent through the real poll
// path, with a fake usage endpoint and a fake OAuth refresh seam. It never
// touches a real credential file or the real OAuth endpoint.
type codexSharedAuthFixture struct {
	fx          *codexManagerFixture
	ag          *CodexAgent
	authPath    string
	profilePath string
	oauthCalls  atomic.Int32
	// responses is consumed one per OAuth refresh call.
	responses []api.CodexOAuthTokenResponse
}

// newCodexSharedAuthFixture starts a "work" profile whose own token is expiring
// and whose refresh token is shared with auth.json, so the first poll borrows
// the (valid) auth.json credentials.
func newCodexSharedAuthFixture(t *testing.T, account, user string, responses ...api.CodexOAuthTokenResponse) *codexSharedAuthFixture {
	t.Helper()

	fx := newCodexManagerFixture(t)

	// auth.json's access token is opaque, so its expiry comes from the id_token:
	// near-expiry (within the 6h refresh threshold) makes the first poll borrow
	// it AND refresh it. The profile's own token is already expired, so the
	// system credentials win the borrow.
	expiringID := makeCodexIDToken(t, time.Now().Add(time.Hour), account, user)
	authPath := writeAuthJSON(t, "sys-access", "shared-r0", expiringID, account)

	profile := CodexProfile{Name: "work", AccountID: account, UserID: user}
	profile.Tokens.AccessToken = "opaque-not-a-jwt"
	profile.Tokens.RefreshToken = "shared-r0"
	profile.Tokens.IDToken = makeCodexIDToken(t, time.Now().Add(-1*time.Hour), account, user)
	profilePath := fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")

	// Stop the background Run goroutine and wait for it to exit, so the test
	// drives poll() itself with no concurrent poll. The manager's polling check
	// is still false here, so the goroutine's own immediate poll returns before
	// it can consume an OAuth response.
	instance := fx.instance("work")
	ag := instance.Agent
	instance.Cancel()
	waitUntil(t, time.Second, func() bool { return fx.instance("work") == nil }, "background work agent to stop")
	ag.pollingCheck = nil

	// Fake usage endpoint: accept any bearer, return a valid payload.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	t.Cleanup(server.Close)
	ag.client = api.NewCodexClient("sys-access", fx.logger, api.WithCodexBaseURL(server.URL))

	f := &codexSharedAuthFixture{
		fx:          fx,
		ag:          ag,
		authPath:    authPath,
		profilePath: profilePath,
		responses:   responses,
	}
	// The seam stands in for the OAuth endpoint. It returns the queued
	// responses in order; the agent persists them via its own tokenSave.
	ag.oauthRefresh = func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		n := int(f.oauthCalls.Add(1))
		if n > len(f.responses) {
			return nil, fmt.Errorf("unexpected OAuth refresh call %d (no queued response)", n)
		}
		resp := f.responses[n-1]
		return &resp, nil
	}
	return f
}

// setSharedTokens rewrites both the profile and auth.json to hold identical
// tokens, modelling the state after a write-back and time passing. Both access
// tokens are the same expiring JWT, which is what makes
// shouldUseSystemCredsForProfile return false on the next refresh.
func (f *codexSharedAuthFixture) setSharedTokens(t *testing.T, accessExpiry time.Duration, refreshToken string) {
	t.Helper()

	const account = "acct-shared"
	const user = "user-shared"
	expiringAccess := makeCodexIDToken(t, time.Now().Add(accessExpiry), account, user)

	// auth.json
	var af authFileFields
	af.Tokens.AccessToken = expiringAccess
	af.Tokens.RefreshToken = refreshToken
	af.Tokens.IDToken = makeCodexIDToken(t, time.Now().Add(accessExpiry), account, user)
	af.Tokens.AccountID = account
	if err := os.WriteFile(f.authPath, mustJSON(t, af), 0o600); err != nil {
		t.Fatalf("rewrite auth.json: %v", err)
	}

	// profile
	profile := readProfile(t, f.profilePath)
	profile.Tokens.AccessToken = expiringAccess
	profile.Tokens.RefreshToken = refreshToken
	profile.Tokens.IDToken = makeCodexIDToken(t, time.Now().Add(accessExpiry), account, user)
	if err := os.WriteFile(f.profilePath, mustJSON(t, profile), 0o600); err != nil {
		t.Fatalf("rewrite profile: %v", err)
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// (a) The reviewer's High scenario end to end: a named profile borrows auth.json,
// the first refresh writes the rotation to both files, later polls see a valid
// token, then a second refresh of the same shared token must still update
// auth.json. A flag cleared once the borrow ended would leave auth.json holding
// the consumed token and log the CLI out.
func TestCodexSharedAuth_LaterRefreshStillUpdatesAuthJSON(t *testing.T) {
	const account = "acct-shared"
	const user = "user-shared"

	firstAccess := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)
	secondAccess := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)

	f := newCodexSharedAuthFixture(t, account, user,
		api.CodexOAuthTokenResponse{AccessToken: firstAccess, RefreshToken: "shared-r1", IDToken: firstAccess, ExpiresIn: 48 * 3600},
		api.CodexOAuthTokenResponse{AccessToken: secondAccess, RefreshToken: "shared-r2", IDToken: secondAccess, ExpiresIn: 48 * 3600},
	)

	ctx := context.Background()

	// First poll: profile is expiring, so the agent borrows the valid auth.json
	// credentials and refreshes the shared token.
	f.ag.poll(ctx)
	if got := readProfile(t, f.profilePath).Tokens.RefreshToken; got != "shared-r1" {
		t.Fatalf("profile refresh_token = %q, want shared-r1", got)
	}
	if got := readAuthRefreshToken(t, f.authPath); got != "shared-r1" {
		t.Fatalf("auth.json refresh_token after borrow = %q, want shared-r1 (rotation must be written back)", got)
	}

	// The token is now valid for 48h: later polls must not refresh.
	for i := 0; i < 3; i++ {
		f.ag.poll(ctx)
	}
	if n := f.oauthCalls.Load(); n != 1 {
		t.Fatalf("OAuth refresh calls = %d, want 1 while the token is valid", n)
	}

	// Time passes: both files now hold identical, near-expiry tokens (the state
	// after a write-back). shouldUseSystemCredsForProfile is false here, so the
	// refresh is sourced from the profile - but auth.json shares the token and
	// must still receive the rotation.
	f.setSharedTokens(t, time.Hour, "shared-r1")

	f.ag.poll(ctx)
	if n := f.oauthCalls.Load(); n != 2 {
		t.Fatalf("OAuth refresh calls = %d, want 2 (second refresh on near-expiry)", n)
	}
	if got := readProfile(t, f.profilePath).Tokens.RefreshToken; got != "shared-r2" {
		t.Fatalf("profile refresh_token after second refresh = %q, want shared-r2", got)
	}
	if got := readAuthRefreshToken(t, f.authPath); got != "shared-r2" {
		t.Fatalf("auth.json refresh_token after second refresh = %q, want shared-r2 (CLI would be logged out)", got)
	}
}

// (b) A failed profile write must not strand the rotation: auth.json still
// receives it when the compare-and-swap matches, and the error is reported.
func TestCodexSharedAuth_ProfileWriteFailureStillUpdatesAuthJSON(t *testing.T) {
	fx := newCodexManagerFixture(t)

	const account = "acct-fail"
	const user = "user-fail"
	validID := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)
	authPath := writeAuthJSON(t, "sys-access", "shared-r0", validID, account)

	profile := CodexProfile{Name: "work", AccountID: account, UserID: user}
	profile.Tokens.AccessToken = "opaque-not-a-jwt"
	profile.Tokens.RefreshToken = "shared-r0"
	profile.Tokens.IDToken = validID
	profilePath := fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")
	agent := fx.instance("work").Agent

	// Make the profile write fail deterministically: replace the profile file
	// with a directory, so saveTokensToProfile's ReadFile errors. This works
	// regardless of the runner's uid (root bypasses read-only permissions).
	if err := os.Remove(profilePath); err != nil {
		t.Fatalf("remove profile: %v", err)
	}
	if err := os.Mkdir(profilePath, 0o700); err != nil {
		t.Fatalf("mkdir over profile path: %v", err)
	}

	// The refresh consumed "shared-r0", which auth.json still holds.
	err := agent.tokenSave("rotated-access", "shared-r1", validID, 604800, "shared-r0")
	if err == nil {
		t.Fatal("expected a combined error when the profile write fails")
	}
	if got := readAuthRefreshToken(t, authPath); got != "shared-r1" {
		t.Fatalf("auth.json refresh_token = %q, want shared-r1 (rotation must survive a failed profile write)", got)
	}
}

// (c) Field preservation: unrelated auth.json fields survive the write-back.
func TestCodexSharedAuth_PreservesAuthJSONFields(t *testing.T) {
	fx := newCodexManagerFixture(t)

	const account = "acct-fields"
	const user = "user-fields"
	validID := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)

	var fields authFileFields
	fields.OpenAIAPIKey = "sk-preserve-me"
	fields.LastRefresh = "2026-01-01T00:00:00Z"
	fields.UnknownExtra = "keep-this"
	fields.Tokens.AccessToken = "sys-access"
	fields.Tokens.RefreshToken = "shared-r0"
	fields.Tokens.IDToken = validID
	fields.Tokens.AccountID = account
	authPath := writeAuthFile(t, fields)

	profile := CodexProfile{Name: "work", AccountID: account, UserID: user}
	profile.Tokens.AccessToken = "opaque-not-a-jwt"
	profile.Tokens.RefreshToken = "shared-r0"
	profile.Tokens.IDToken = validID
	fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")
	agent := fx.instance("work").Agent

	if err := agent.tokenSave("rotated-access", "shared-r1", validID, 604800, "shared-r0"); err != nil {
		t.Fatalf("tokenSave: %v", err)
	}

	got := readAuthFile(t, authPath)
	if got.Tokens.RefreshToken != "shared-r1" {
		t.Fatalf("refresh_token = %q, want shared-r1", got.Tokens.RefreshToken)
	}
	if got.OpenAIAPIKey != "sk-preserve-me" {
		t.Errorf("OPENAI_API_KEY = %q, want preserved", got.OpenAIAPIKey)
	}
	if got.Tokens.AccountID != account {
		t.Errorf("account_id = %q, want %q", got.Tokens.AccountID, account)
	}
	if got.LastRefresh != "2026-01-01T00:00:00Z" {
		t.Errorf("last_refresh = %q, want preserved", got.LastRefresh)
	}
	if got.UnknownExtra != "keep-this" {
		t.Errorf("unknown extra field = %q, want preserved", got.UnknownExtra)
	}
}

// (d) A newer CLI login (a different refresh token in auth.json) is never
// overwritten by onWatch's rotation.
func TestCodexSharedAuth_NewerCLILoginIsNotOverwritten(t *testing.T) {
	fx := newCodexManagerFixture(t)

	const account = "acct-race"
	const user = "user-race"
	validID := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)
	authPath := writeAuthJSON(t, "cli-access", "cli-newer-refresh", validID, account)

	profile := CodexProfile{Name: "work", AccountID: account, UserID: user}
	profile.Tokens.AccessToken = "opaque-not-a-jwt"
	profile.Tokens.RefreshToken = "shared-r0"
	profile.Tokens.IDToken = validID
	fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")
	agent := fx.instance("work").Agent

	// The refresh consumed "shared-r0", but auth.json now holds the CLI's newer
	// token: the compare-and-swap must not match.
	if err := agent.tokenSave("rotated-access", "shared-r1", validID, 604800, "shared-r0"); err != nil {
		t.Fatalf("tokenSave: %v", err)
	}
	if got := readAuthRefreshToken(t, authPath); got != "cli-newer-refresh" {
		t.Fatalf("auth.json refresh_token = %q, want cli-newer-refresh (must not overwrite a newer login)", got)
	}
}

// A named profile can share its refresh token with the OpenCode auth store
// (DetectCodexCredentials falls back to it), so a rotation must be written back
// there too, in OpenCode's own format.
func TestCodexSharedAuth_OpenCodeSourceWriteBack(t *testing.T) {
	fx := newCodexManagerFixture(t)

	const account = "acct-oc"
	const user = "user-oc"
	validID := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)

	// No Codex auth.json; the shared token lives in OpenCode's store.
	ocPath := api.OpenCodeAuthPath()
	if ocPath == "" {
		t.Skip("OpenCode auth path unavailable in this environment")
	}
	if err := os.MkdirAll(filepath.Dir(ocPath), 0o700); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	ocRaw := `{"openai":{"type":"oauth","access":"oc-access","refresh":"shared-r0","expires":1,"accountId":"keep_me"},"other":"preserved"}`
	if err := os.WriteFile(ocPath, []byte(ocRaw), 0o600); err != nil {
		t.Fatalf("write opencode auth: %v", err)
	}

	profile := CodexProfile{Name: "work", AccountID: account, UserID: user}
	profile.Tokens.AccessToken = "opaque-not-a-jwt"
	profile.Tokens.RefreshToken = "shared-r0"
	profile.Tokens.IDToken = validID
	fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")
	agent := fx.instance("work").Agent

	if err := agent.tokenSave("rotated-access", "shared-r1", validID, 604800, "shared-r0"); err != nil {
		t.Fatalf("tokenSave: %v", err)
	}

	data, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatalf("read opencode auth: %v", err)
	}
	var oc struct {
		OpenAI struct {
			Refresh   string `json:"refresh"`
			AccountID string `json:"accountId"`
		} `json:"openai"`
		Other string `json:"other"`
	}
	if err := json.Unmarshal(data, &oc); err != nil {
		t.Fatalf("unmarshal opencode auth: %v", err)
	}
	if oc.OpenAI.Refresh != "shared-r1" {
		t.Fatalf("opencode refresh = %q, want shared-r1 (rotation must reach the OpenCode store)", oc.OpenAI.Refresh)
	}
	if oc.OpenAI.AccountID != "keep_me" || oc.Other != "preserved" {
		t.Fatalf("opencode unrelated fields lost: %s", data)
	}
}

// A profile using its own token (no shared auth.json) must leave auth.json
// byte-identical.
func TestCodexSharedAuth_UnrelatedAuthJSONUntouched(t *testing.T) {
	fx := newCodexManagerFixture(t)

	const account = "acct-own"
	const user = "user-own"
	validID := makeCodexIDToken(t, time.Now().Add(48*time.Hour), account, user)
	authPath := writeAuthJSON(t, "other-access", "other-refresh", validID, "acct-other")
	before, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}

	profile := CodexProfile{Name: "work", AccountID: account, UserID: user}
	profile.Tokens.AccessToken = "own-access"
	profile.Tokens.RefreshToken = "own-refresh"
	profile.Tokens.IDToken = validID
	fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")
	agent := fx.instance("work").Agent

	if err := agent.tokenSave("rotated-access", "rotated-refresh", validID, 604800, "own-refresh"); err != nil {
		t.Fatalf("tokenSave: %v", err)
	}

	after, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("unrelated auth.json changed:\ngot: %s\nwant: %s", after, before)
	}
}
