package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshCodexToken_Success(t *testing.T) {
	// Create a mock OAuth server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("expected form-encoded content type, got %s", r.Header.Get("Content-Type"))
		}

		// Parse form
		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}

		// Verify form values
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("expected grant_type=refresh_token, got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("client_id") != CodexOAuthClientID {
			t.Errorf("expected client_id=%s, got %s", CodexOAuthClientID, r.FormValue("client_id"))
		}
		if r.FormValue("refresh_token") != "test-refresh-token" {
			t.Errorf("expected refresh_token=test-refresh-token, got %s", r.FormValue("refresh_token"))
		}

		// Return success response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CodexOAuthTokenResponse{
			TokenType:    "Bearer",
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    604800,
			IDToken:      "new-id-token",
			Scope:        CodexOAuthScope,
		})
	}))
	defer server.Close()

	resp, err := RefreshCodexTokenWithURL(context.Background(), "test-refresh-token", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.AccessToken != "new-access-token" {
		t.Errorf("expected access_token=new-access-token, got %s", resp.AccessToken)
	}
	if resp.RefreshToken != "new-refresh-token" {
		t.Errorf("expected refresh_token=new-refresh-token, got %s", resp.RefreshToken)
	}
	if resp.ExpiresIn != 604800 {
		t.Errorf("expected expires_in=604800, got %d", resp.ExpiresIn)
	}
}

func TestRefreshCodexToken_RefreshTokenReused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "refresh token has been reused",
		})
	}))
	defer server.Close()

	_, err := RefreshCodexTokenWithURL(context.Background(), "reused-refresh-token", server.URL)
	if err == nil {
		t.Fatal("expected error for reused refresh token")
	}

	if !errors.Is(err, ErrCodexRefreshTokenReused) {
		t.Errorf("expected ErrCodexRefreshTokenReused, got %v", err)
	}
}

func TestRefreshCodexToken_GenericError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_request",
			"error_description": "missing required field",
		})
	}))
	defer server.Close()

	_, err := RefreshCodexTokenWithURL(context.Background(), "bad-token", server.URL)
	if err == nil {
		t.Fatal("expected error")
	}

	if !errors.Is(err, ErrCodexOAuthRefreshFailed) {
		t.Errorf("expected ErrCodexOAuthRefreshFailed, got %v", err)
	}
}

// A bare 401 from the token endpoint (no JSON error body) is the production
// signature of a dead refresh token and must be terminal, not retryable
// (issue #147 review, item 2).
func TestRefreshCodexToken_UnauthorizedIsTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := RefreshCodexTokenWithURL(context.Background(), "dead-token", server.URL)
	if err == nil {
		t.Fatal("expected error for a 401 response")
	}
	if !errors.Is(err, ErrCodexRefreshTokenInvalid) {
		t.Errorf("expected ErrCodexRefreshTokenInvalid, got %v", err)
	}
}

// A 5xx from the token endpoint is transient, not terminal.
func TestRefreshCodexToken_ServerErrorIsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := RefreshCodexTokenWithURL(context.Background(), "good-token", server.URL)
	if err == nil {
		t.Fatal("expected error for a 503 response")
	}
	if errors.Is(err, ErrCodexRefreshTokenInvalid) || errors.Is(err, ErrCodexRefreshTokenReused) {
		t.Errorf("a 503 must be transient, got %v", err)
	}
	if !errors.Is(err, ErrCodexOAuthRefreshFailed) {
		t.Errorf("expected ErrCodexOAuthRefreshFailed, got %v", err)
	}
}

func TestRefreshCodexToken_EmptyAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CodexOAuthTokenResponse{
			TokenType:    "Bearer",
			AccessToken:  "", // Empty!
			RefreshToken: "new-refresh-token",
		})
	}))
	defer server.Close()

	_, err := RefreshCodexTokenWithURL(context.Background(), "test-token", server.URL)
	if err == nil {
		t.Fatal("expected error for empty access token")
	}

	if !errors.Is(err, ErrCodexOAuthRefreshFailed) {
		t.Errorf("expected ErrCodexOAuthRefreshFailed, got %v", err)
	}
}

func TestRefreshCodexToken_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := RefreshCodexTokenWithURL(ctx, "test-token", server.URL)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestParseIDTokenExpiry_ValidJWT(t *testing.T) {
	// Create a valid JWT-like structure with an exp claim
	// Header: {"alg":"none"}
	// Payload: {"exp":1893456000} (2030-01-01 00:00:00 UTC)
	// Signature: (empty)
	header := "eyJhbGciOiJub25lIn0"       // {"alg":"none"} base64url encoded
	payload := "eyJleHAiOjE4OTM0NTYwMDB9" // {"exp":1893456000} base64url encoded
	idToken := header + "." + payload + "."

	expiry := ParseIDTokenExpiry(idToken)

	expected := time.Unix(1893456000, 0)
	if !expiry.Equal(expected) {
		t.Errorf("expected expiry %v, got %v", expected, expiry)
	}
}

func TestParseIDTokenExpiry_EmptyToken(t *testing.T) {
	expiry := ParseIDTokenExpiry("")
	if !expiry.IsZero() {
		t.Errorf("expected zero time for empty token, got %v", expiry)
	}
}

func TestParseIDTokenExpiry_InvalidFormat(t *testing.T) {
	expiry := ParseIDTokenExpiry("not.a.valid.jwt.token")
	if !expiry.IsZero() {
		t.Errorf("expected zero time for invalid token, got %v", expiry)
	}
}

func TestParseIDTokenExpiry_NoExpClaim(t *testing.T) {
	// Create JWT without exp claim
	// Payload: {"sub":"user123"}
	header := "eyJhbGciOiJub25lIn0"
	payload := "eyJzdWIiOiJ1c2VyMTIzIn0" // {"sub":"user123"} base64url encoded
	idToken := header + "." + payload + "."

	expiry := ParseIDTokenExpiry(idToken)
	if !expiry.IsZero() {
		t.Errorf("expected zero time for token without exp, got %v", expiry)
	}
}

func TestParseIDTokenUserID_ValidJWT(t *testing.T) {
	header := "eyJhbGciOiJub25lIn0"
	payloadJSON := `{"https://api.openai.com/auth":{"chatgpt_user_id":"user-A49xYQ0FVIVFBd9E3FIkDYBh"}}`
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	idToken := header + "." + payload + "."

	userID := ParseIDTokenUserID(idToken)
	if userID != "user-A49xYQ0FVIVFBd9E3FIkDYBh" {
		t.Fatalf("ParseIDTokenUserID() = %q, want user-A49xYQ0FVIVFBd9E3FIkDYBh", userID)
	}
}

func TestParseIDTokenUserID_MissingClaim(t *testing.T) {
	header := "eyJhbGciOiJub25lIn0"
	payloadJSON := `{"https://api.openai.com/auth":{"chatgpt_account_id":"acc-123"}}`
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	idToken := header + "." + payload + "."

	userID := ParseIDTokenUserID(idToken)
	if userID != "" {
		t.Fatalf("ParseIDTokenUserID() = %q, want empty", userID)
	}
}

func TestParseIDTokenUserID_InvalidJWT(t *testing.T) {
	userID := ParseIDTokenUserID("not.a.valid.jwt.token")
	if userID != "" {
		t.Fatalf("ParseIDTokenUserID() = %q, want empty", userID)
	}
}

func TestParseIDTokenUserID_EmptyToken(t *testing.T) {
	userID := ParseIDTokenUserID("")
	if userID != "" {
		t.Fatalf("ParseIDTokenUserID() = %q, want empty", userID)
	}
}

// makeExpJWT builds a minimal unsigned JWT carrying only an exp claim.
func makeExpJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]int64{"exp": exp.Unix()})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + "."
}

// The access token is the source of truth for Codex credential expiry. An
// id_token that expires sooner must not win (issue #147).
func TestCodexTokenExpiry_PrefersAccessToken(t *testing.T) {
	access := makeExpJWT(t, time.Unix(1893456000, 0))
	id := makeExpJWT(t, time.Unix(1000000000, 0))

	if got := CodexTokenExpiry(access, id); !got.Equal(time.Unix(1893456000, 0)) {
		t.Fatalf("CodexTokenExpiry = %v, want the access token expiry", got)
	}
}

// An unparseable access token falls back to the id_token expiry.
func TestCodexTokenExpiry_FallsBackToIDToken(t *testing.T) {
	id := makeExpJWT(t, time.Unix(1893456000, 0))

	if got := CodexTokenExpiry("opaque-token", id); !got.Equal(time.Unix(1893456000, 0)) {
		t.Fatalf("CodexTokenExpiry = %v, want the id token expiry", got)
	}
}

func TestCodexTokenExpiry_BothUnparseable(t *testing.T) {
	if got := CodexTokenExpiry("opaque", "also-opaque"); !got.IsZero() {
		t.Fatalf("CodexTokenExpiry = %v, want zero", got)
	}
}
