package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The token endpoint answers any "claude-code/2.1.x" User-Agent with 429 before
// it looks at the refresh token, so proactive refresh could never succeed and
// polling paused once the access token expired (issue #145). The endpoint
// accepts the "claude-cli/<version> (external, cli)" form.
func TestRefreshAnthropicToken_RequestShape(t *testing.T) {
	var (
		gotMethod, gotContentType, gotBeta, gotUA string
		gotBody                                   oauthRefreshRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBeta = r.Header.Get("anthropic-beta")
		gotUA = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token_type":"Bearer","access_token":"new-access","refresh_token":"new-refresh","expires_in":28800}`))
	}))
	defer server.Close()

	origURL := anthropicOAuthTokenURL
	defer func() { resetOAuthURL(origURL) }()
	setOAuthURL(server.URL)

	if _, err := RefreshAnthropicToken(context.Background(), "test-refresh-token"); err != nil {
		t.Fatalf("RefreshAnthropicToken() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBeta != anthropicOAuthBeta {
		t.Errorf("anthropic-beta = %q, want %q", gotBeta, anthropicOAuthBeta)
	}
	if want := "claude-cli/2.1.289 (external, cli)"; gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
	if gotBody.GrantType != "refresh_token" || gotBody.RefreshToken != "test-refresh-token" || gotBody.ClientID != AnthropicOAuthClientID {
		t.Errorf("request body = %+v, want refresh_token grant for the Claude Code client", gotBody)
	}
}
