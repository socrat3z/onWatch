package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCredsFixture(t *testing.T, dir, access, refresh string) string {
	t.Helper()
	path := filepath.Join(dir, ".credentials.json")
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  access,
			"refreshToken": refresh,
			"expiresAt":    time.Now().Add(time.Minute).UnixMilli(),
			"scopes":       []string{"user:inference"},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func oauthStub(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	SetOAuthURLForTest(srv.URL)
	t.Cleanup(func() { SetOAuthURLForTest(AnthropicOAuthTokenURL) })
}

// TestRefreshAnthropicCredentialsFile_AdoptsNewerGeneration verifies that a pair
// another writer already rotated is taken as-is, rather than spending our own
// now-superseded refresh token on top of it.
func TestRefreshAnthropicCredentialsFile_AdoptsNewerGeneration(t *testing.T) {
	called := false
	oauthStub(t, func(w http.ResponseWriter, r *http.Request) { called = true })

	path := writeCredsFixture(t, t.TempDir(), "disk-access", "disk-refresh")
	rot, err := RefreshAnthropicCredentialsFile(context.Background(), path, "our-stale-access")
	if err != nil {
		t.Fatalf("RefreshAnthropicCredentialsFile: %v", err)
	}
	if called {
		t.Error("token exchange was attempted, want the stored pair adopted instead")
	}
	if !rot.Adopted || !rot.Persisted {
		t.Errorf("Adopted=%v Persisted=%v, want both true", rot.Adopted, rot.Persisted)
	}
	if rot.Tokens.AccessToken != "disk-access" || rot.Tokens.RefreshToken != "disk-refresh" {
		t.Errorf("adopted pair = %q/%q, want disk-access/disk-refresh", rot.Tokens.AccessToken, rot.Tokens.RefreshToken)
	}
	if rot.Tokens.ExpiresIn <= 0 {
		t.Errorf("ExpiresIn = %d, want a positive remaining lifetime", rot.Tokens.ExpiresIn)
	}
}

// TestRefreshAnthropicCredentialsFile_PreservesOmittedRefreshToken covers RFC
// 6749 section 6: refresh_token is optional in a successful response, and the
// existing token stays current when the server omits it.
func TestRefreshAnthropicCredentialsFile_PreservesOmittedRefreshToken(t *testing.T) {
	oauthStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"token_type":   "bearer",
			"access_token": "rotated-access",
			"expires_in":   3600,
		})
	})

	path := writeCredsFixture(t, t.TempDir(), "old-access", "keep-this-refresh")
	rot, err := RefreshAnthropicCredentialsFile(context.Background(), path, "old-access")
	if err != nil {
		t.Fatalf("RefreshAnthropicCredentialsFile: %v", err)
	}
	if rot.Tokens.RefreshToken != "keep-this-refresh" {
		t.Errorf("RefreshToken = %q, want the preserved keep-this-refresh", rot.Tokens.RefreshToken)
	}
	if !rot.Persisted || rot.PersistErr != nil {
		t.Errorf("Persisted=%v PersistErr=%v, want true/nil", rot.Persisted, rot.PersistErr)
	}

	stored, err := ReadAnthropicCredentialsFile(path)
	if err != nil {
		t.Fatalf("ReadAnthropicCredentialsFile: %v", err)
	}
	if stored.AccessToken != "rotated-access" || stored.RefreshToken != "keep-this-refresh" {
		t.Errorf("stored pair = %q/%q, want rotated-access/keep-this-refresh", stored.AccessToken, stored.RefreshToken)
	}
}

// TestRefreshAnthropicCredentialsFile_LockContentionTimesOut verifies that a
// peer holding the lock produces a bounded, transient error instead of blocking
// the caller's poll goroutine forever.
func TestRefreshAnthropicCredentialsFile_LockContentionTimesOut(t *testing.T) {
	path := writeCredsFixture(t, t.TempDir(), "access", "refresh")

	unlock, err := tryLockAnthropicCredentials(path)
	if err != nil {
		t.Fatalf("tryLockAnthropicCredentials: %v", err)
	}
	if unlock == nil {
		t.Fatal("lock was unexpectedly unavailable")
	}
	defer unlock()

	// A second attempt must observe the lock as held rather than acquiring it.
	again, err := tryLockAnthropicCredentials(path)
	if err != nil {
		t.Fatalf("second tryLockAnthropicCredentials: %v", err)
	}
	if again != nil {
		again()
		t.Fatal("acquired a lock already held by this process")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := RefreshAnthropicCredentialsFile(ctx, path, "access"); err == nil {
		t.Fatal("expected an error while the lock is held")
	} else if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrCredentialsLocked) {
		t.Fatalf("error = %v, want a deadline or ErrCredentialsLocked", err)
	}
}
