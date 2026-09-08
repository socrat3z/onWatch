package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// AnthropicRotation is the outcome of one credential rotation transaction.
type AnthropicRotation struct {
	// Tokens is the pair the agent should poll with. Never nil on success.
	Tokens *OAuthTokenResponse
	// Adopted reports that another writer had already rotated the stored
	// credentials, so Tokens came off disk instead of from a token exchange.
	Adopted bool
	// Persisted reports that Tokens are already on disk. When false the caller
	// still owns writing them - the refresh token has been consumed either way,
	// so dropping the pair here would strand the account.
	Persisted bool
	// PersistErr is the write error when the transaction exchanged tokens but
	// could not store them. Advisory: the caller retries the write itself.
	PersistErr error
}

// remainingSeconds converts a stored expiry into an ExpiresIn value, clamping
// unknown (zero) and past expiries to 0 rather than a negative duration.
func remainingSeconds(expiresAt time.Time) int {
	if expiresAt.IsZero() {
		return 0
	}
	if remaining := int(time.Until(expiresAt).Seconds()); remaining > 0 {
		return remaining
	}
	return 0
}

// adopt wraps another writer's stored pair as an already-persisted rotation.
func adopt(creds *AnthropicCredentials) AnthropicRotation {
	return AnthropicRotation{
		Tokens: &OAuthTokenResponse{
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			ExpiresIn:    remainingSeconds(creds.ExpiresAt),
		},
		Adopted:   true,
		Persisted: true,
	}
}

// RefreshAnthropicCredentialsFile serializes the complete read/exchange/write
// transaction for one Claude credential file. If another cooperating writer
// rotated the credentials while the caller was waiting, its pair is adopted
// instead of reusing the now-consumed refresh token.
func RefreshAnthropicCredentialsFile(ctx context.Context, path, expectedAccess string) (AnthropicRotation, error) {
	unlock, err := lockAnthropicCredentials(ctx, path)
	if err != nil {
		return AnthropicRotation{}, fmt.Errorf("lock credentials: %w", err)
	}
	defer unlock()

	current, err := ReadAnthropicCredentialsFile(path)
	if err != nil {
		return AnthropicRotation{}, err
	}
	if current == nil || current.RefreshToken == "" {
		return AnthropicRotation{}, errors.New("credentials have no refresh token")
	}
	if expectedAccess != "" && current.AccessToken != expectedAccess {
		return adopt(current), nil
	}

	refreshed, err := RefreshAnthropicToken(ctx, current.RefreshToken)
	if err != nil {
		// invalid_grant can mean a non-cooperating writer won the race. Re-read
		// once and adopt its generation before declaring the login terminal. A
		// pair with no refresh token is not worth adopting: it would buy one
		// access-token lifetime and then fail terminally anyway.
		if errors.Is(err, ErrOAuthInvalidGrant) {
			latest, readErr := ReadAnthropicCredentialsFile(path)
			if readErr == nil && latest != nil && latest.RefreshToken != "" &&
				latest.AccessToken != current.AccessToken {
				return adopt(latest), nil
			}
		}
		return AnthropicRotation{}, err
	}
	// RFC 6749 makes refresh_token optional in a successful refresh response.
	// Preserve the current token unless the server explicitly rotates it.
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	// From here the old refresh token is spent, so the pair must reach the
	// caller even when the write fails - returning an error instead would see
	// it discarded along with the only credentials that still work.
	if err := WriteAnthropicCredentialsFile(path, refreshed.AccessToken, refreshed.RefreshToken, refreshed.ExpiresIn); err != nil {
		return AnthropicRotation{Tokens: refreshed, PersistErr: err}, nil
	}
	return AnthropicRotation{Tokens: refreshed, Persisted: true}, nil
}

// RefreshAnthropicCredentialsAmbient runs the same serialized transaction for
// the ambient single-account store. The lock file sits beside
// ~/.claude/.credentials.json - the one path every Claude Code process agrees
// on - so it coordinates with the CLI even where the effective store is a
// keychain and the file is only a mirror.
func RefreshAnthropicCredentialsAmbient(ctx context.Context, logger *slog.Logger, expectedAccess string) (AnthropicRotation, error) {
	path := getCredentialsFilePath()
	if path == "" {
		return AnthropicRotation{}, errors.New("no ambient credentials path")
	}
	unlock, err := lockAnthropicCredentials(ctx, path)
	if err != nil {
		return AnthropicRotation{}, fmt.Errorf("lock credentials: %w", err)
	}
	defer unlock()

	current := DetectAnthropicCredentials(logger)
	if current == nil || current.RefreshToken == "" {
		return AnthropicRotation{}, errors.New("credentials have no refresh token")
	}
	if expectedAccess != "" && current.AccessToken != expectedAccess {
		return adopt(current), nil
	}

	refreshed, err := RefreshAnthropicToken(ctx, current.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrOAuthInvalidGrant) {
			if latest := DetectAnthropicCredentials(logger); latest != nil &&
				latest.RefreshToken != "" && latest.AccessToken != current.AccessToken {
				return adopt(latest), nil
			}
		}
		return AnthropicRotation{}, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	if err := WriteAnthropicCredentials(refreshed.AccessToken, refreshed.RefreshToken, refreshed.ExpiresIn); err != nil {
		return AnthropicRotation{Tokens: refreshed, PersistErr: err}, nil
	}
	return AnthropicRotation{Tokens: refreshed, Persisted: true}, nil
}

// claudeCredentials represents the Claude Code credentials JSON structure.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken      string   `json:"accessToken"`
		RefreshToken     string   `json:"refreshToken"`
		ExpiresAt        int64    `json:"expiresAt"` // Unix milliseconds
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
		RateLimitTier    string   `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

// AnthropicCredentials contains the parsed OAuth credentials with computed fields.
type AnthropicCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	ExpiresIn    time.Duration // time until expiry
	Scopes       []string
}

// IsExpiringSoon returns true if the token expires within the given duration.
// Returns false if expiry is unknown (zero ExpiresAt) to avoid spurious refreshes.
func (c *AnthropicCredentials) IsExpiringSoon(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return c.ExpiresIn < threshold
}

// IsExpired returns true if the token has already expired.
func (c *AnthropicCredentials) IsExpired() bool {
	return c.ExpiresIn <= 0
}

// parseClaudeCredentials extracts the OAuth access token from Claude Code credentials JSON.
func parseClaudeCredentials(data []byte) (string, error) {
	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", err
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}

// parseFullClaudeCredentials extracts all OAuth fields from Claude Code credentials JSON.
func parseFullClaudeCredentials(data []byte) (*AnthropicCredentials, error) {
	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}

	oauth := creds.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return nil, nil // no credentials
	}

	// Convert expiresAt from Unix milliseconds to time.Time
	expiresAt := time.UnixMilli(oauth.ExpiresAt)
	expiresIn := time.Until(expiresAt)

	return &AnthropicCredentials{
		AccessToken:  oauth.AccessToken,
		RefreshToken: oauth.RefreshToken,
		ExpiresAt:    expiresAt,
		ExpiresIn:    expiresIn,
		Scopes:       oauth.Scopes,
	}, nil
}

// DetectAnthropicToken attempts to auto-detect the Anthropic OAuth token
// from the Claude Code credentials stored in the system keychain or file.
// Returns empty string if not found.
func DetectAnthropicToken(logger *slog.Logger) string {
	return detectAnthropicTokenPlatform(logger)
}

// DetectAnthropicCredentials attempts to auto-detect the full Anthropic OAuth credentials
// from the Claude Code credentials stored in the system keychain or file.
// Returns nil if not found.
func DetectAnthropicCredentials(logger *slog.Logger) *AnthropicCredentials {
	return detectAnthropicCredentialsPlatform(logger)
}

// ReadAnthropicCredentialsFile reads one explicit Claude Code home. It is used
// by named-account agents and deliberately bypasses ambient keychains so a
// refresh for one alias cannot affect another alias.
func ReadAnthropicCredentialsFile(path string) (*AnthropicCredentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseFullClaudeCredentials(data)
}

// WriteAnthropicCredentialsFile atomically persists rotated credentials to one
// explicit account file. It preserves any fields Claude Code owns.
func WriteAnthropicCredentialsFile(path, accessToken, refreshToken string, expiresIn int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	oauth, _ := raw["claudeAiOauth"].(map[string]interface{})
	if oauth == nil {
		oauth = make(map[string]interface{})
		raw["claudeAiOauth"] = oauth
	}
	oauth["accessToken"] = accessToken
	oauth["refreshToken"] = refreshToken
	oauth["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	// No MkdirAll here: the ReadFile above already proved the directory exists.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
