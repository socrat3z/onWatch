// Package agent provides the background polling agent for onWatch.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// TokenRefreshFunc is called before each poll to get a fresh token.
// Returns the new token, or empty string if refresh is not needed/available.
type TokenRefreshFunc func() string

// CredentialsRefreshFunc returns the full credentials for proactive OAuth refresh.
type CredentialsRefreshFunc func() *api.AnthropicCredentials
type CredentialsWriteFunc func(accessToken, refreshToken string, expiresIn int) error
type CredentialsRotateFunc func(context.Context, string) (api.AnthropicRotation, error)

// maxAuthFailures is the number of consecutive auth failures before pausing polling.
const maxAuthFailures = 3

// maxRateLimitFailures is the number of consecutive OAuth 429s before entering extended backoff.
const maxRateLimitFailures = 5

// rateLimitBaseBackoff is the initial backoff duration after an OAuth 429.
const rateLimitBaseBackoff = 5 * time.Minute

// rateLimitMaxBackoff is the maximum backoff duration for OAuth 429 errors.
const rateLimitMaxBackoff = 6 * time.Hour

// tokenRefreshThreshold is how soon before expiry we proactively refresh the token.
// Kept wide (compare: Codex uses 6h) so the refresh is not racing a narrow window
// that a single skipped poll - or a short-lived Claude Code session - can close.
const tokenRefreshThreshold = 1 * time.Hour

// authPausedRetryInterval is the first delay before a paused agent re-attempts
// OAuth recovery on its own, without waiting for Claude Code to rewrite the
// stored credentials. Doubles per attempt up to authPausedRetryMaxInterval.
const authPausedRetryInterval = 15 * time.Minute

// authPausedRetryMaxInterval caps the escalating paused-state retry delay.
const authPausedRetryMaxInterval = 6 * time.Hour

// rateLimitEscalateAfter is how long an unbroken run of OAuth 429s must last
// before onWatch raises an alert. Measured in wall clock rather than attempts
// because the attempt count resets on any successful poll and on restart, so a
// restarting daemon would otherwise never reach a count-based threshold.
const rateLimitEscalateAfter = 1 * time.Hour

// AnthropicAgent manages the background polling loop for Anthropic quota tracking.
type AnthropicAgent struct {
	client       *api.AnthropicClient
	store        *store.Store
	tracker      *tracker.AnthropicTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	tokenRefresh TokenRefreshFunc
	credsRefresh CredentialsRefreshFunc
	credsWrite   CredentialsWriteFunc
	credsRotate  CredentialsRotateFunc
	lastToken    string
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
	accountID    int64
	accountName  string

	// Auth failure rate limiting
	authFailCount   int    // consecutive auth failures (401 or 403)
	authPaused      bool   // true when polling is paused due to auth failures
	lastFailedToken string // token that caused the failures (to detect credential refresh)

	// Bounded self-recovery while authPaused: without it the only way out is
	// Claude Code rewriting credentials on its own schedule (issue #111).
	authRetryAt    time.Time // next OAuth recovery attempt while paused
	authRetryCount int       // consecutive paused-state recovery attempts

	// supersededToken is an access token known to be stale because an OAuth
	// refresh replaced it but persisting the new pair failed. The pre-poll
	// credential re-read must not downgrade back to it. Empty when the stored
	// credentials are known good.
	supersededToken string

	// lastRefreshBlockReason is why the most recent OAuth refresh was skipped.
	// Reported when polling pauses, so the pause always states its cause.
	lastRefreshBlockReason string

	// OAuth rate limit backoff (429 from the OAuth refresh endpoint)
	rateLimitFailCount int       // consecutive OAuth 429 failures
	rateLimitPaused    bool      // true when OAuth refresh is in backoff
	rateLimitResumeAt  time.Time // when to next attempt OAuth refresh

	// Escalation for a sustained 429 run. Backoff alone only answers how often
	// to retry, never whether retrying can still work: a persistent block paces
	// itself out to rateLimitMaxBackoff and stays silent indefinitely. These
	// track the run by wall clock rather than by count, because the count is
	// reset by any successful poll and by a daemon restart.
	rateLimitFirstFailAt time.Time // start of the current run of 429s
	rateLimitNotified    bool      // alert already sent for the current run

	// isClaudeCodeRunning checks if Claude Code is executing. If nil, uses the
	// package-level IsClaudeCodeRunning. Override in tests to control behavior.
	isClaudeCodeRunning func() bool

	// Statusline bridge: reads Anthropic rate limits from Claude Code's statusline
	// output file, avoiding the rate-limited usage API entirely.
	statuslinePath      string        // path to statusline JSON file
	statuslineStaleness time.Duration // max age before falling back to API

	// Hybrid polling: in auto mode, do a full API poll every N cycles to get
	// supplementary quotas (seven_day_sonnet, per-model weekly, extra_usage)
	// that the statusline doesn't provide. 0 = disabled.
	apiPollCycleInterval int // API poll every N cycles (default: 10)
	pollCycleCount       int // current cycle counter

	// lastAPIPoll is when the OAuth usage API was last called. It is seeded
	// from the newest stored API snapshot on first use, so the schedule
	// survives a restart instead of starting over from zero.
	lastAPIPoll     time.Time
	lastAPIPollRead bool // lastAPIPoll has been seeded from the store

	// apiPollingDisabled is set in "statusline only" mode. When true the agent
	// never calls the OAuth usage API - not even as a fallback when the
	// statusline file is stale or missing. Default false preserves API polling
	// for "auto" and "api" modes.
	apiPollingDisabled bool
}

// SetPollingCheck sets a function that is called before each poll.
// If it returns false, the poll is skipped (provider polling disabled).
func (a *AnthropicAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetAccountContext assigns every stored snapshot and log entry to one
// provider-account record. The ambient agent retains the zero/default value.
func (a *AnthropicAgent) SetAccountContext(accountID int64, accountName string) {
	a.accountID = accountID
	a.accountName = accountName
}

// SetNotifier sets the notification engine for sending alerts.
func (a *AnthropicAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// sendAuthErrorNotification sends an auth error notification via the notifier.
func (a *AnthropicAgent) sendAuthErrorNotification(title, message string, isRecoverable bool) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendAuthErrorNotification(notify.AuthErrorAlert{
		Provider:    "anthropic",
		Title:       title,
		Message:     message,
		IsRecovable: isRecoverable,
	})
}

// NewAnthropicAgent creates a new AnthropicAgent with the given dependencies.
func NewAnthropicAgent(client *api.AnthropicClient, store *store.Store, tr *tracker.AnthropicTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *AnthropicAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &AnthropicAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// SetTokenRefresh sets a function that will be called before each poll to
// refresh the Anthropic OAuth token. This enables automatic token rotation
// when Claude Code refreshes credentials on disk.
func (a *AnthropicAgent) SetTokenRefresh(fn TokenRefreshFunc) {
	a.tokenRefresh = fn
}

// SetCredentialsRefresh sets a function that returns full credentials for
// proactive OAuth token refresh before expiry.
func (a *AnthropicAgent) SetCredentialsRefresh(fn CredentialsRefreshFunc) {
	a.credsRefresh = fn
}

// SetCredentialsWriter persists OAuth rotation for this agent's exact account.
func (a *AnthropicAgent) SetCredentialsWriter(fn CredentialsWriteFunc) { a.credsWrite = fn }

// SetCredentialsRotator installs an account-scoped, cross-process-safe OAuth
// transaction. The string argument is the access-token generation the caller
// observed; the bool result reports that a newer stored generation was adopted.
func (a *AnthropicAgent) SetCredentialsRotator(fn CredentialsRotateFunc) { a.credsRotate = fn }

// EnableStatuslineBridge activates the statusline file bridge for zero-429
// Anthropic monitoring. When enabled, the agent checks a shared file written
// by Claude Code's statusline before falling back to the OAuth usage API.
// Must be called explicitly - not enabled by default to avoid test interference.
func (a *AnthropicAgent) EnableStatuslineBridge() {
	a.statuslinePath = StatuslineDataPath()
	a.statuslineStaleness = statuslineStalenessDefault
	a.apiPollCycleInterval = 10 // default: full API poll every 10 cycles
}

// SetAPIPollCycleInterval sets how often (in cycles) a full API poll is done
// alongside statusline data to get supplementary quotas like seven_day_sonnet.
// 0 disables periodic API polling. Default is 10 (every 10th cycle).
func (a *AnthropicAgent) SetAPIPollCycleInterval(n int) {
	a.apiPollCycleInterval = n
}

// apiPollInterval is how long a stored API reading stays fresh enough to skip
// the next supplementary poll.
func (a *AnthropicAgent) apiPollInterval() time.Duration {
	if a.apiPollCycleInterval <= 0 {
		return 0
	}
	return time.Duration(a.apiPollCycleInterval) * a.interval
}

// beginSupplementalAPIPoll reports whether the OAuth usage API is due, and
// claims the slot when it is.
//
// The schedule is driven by the age of the newest stored API reading rather
// than by a counter held in memory. A counter restarts at zero on every daemon
// start, which used to leave per-model weekly quotas - often the binding limit
// - unreachable for a full interval after any restart or upgrade.
//
// The slot is claimed on the attempt, not on success: a failing API must not be
// retried on every poll, because the usage API answers repeated calls with 429.
func (a *AnthropicAgent) beginSupplementalAPIPoll() bool {
	interval := a.apiPollInterval()
	if interval <= 0 {
		return false
	}
	a.seedLastAPIPoll()
	if !a.lastAPIPoll.IsZero() && time.Since(a.lastAPIPoll) < interval {
		return false
	}
	a.noteAPIPoll()
	return true
}

// seedLastAPIPoll reads the newest stored API snapshot once per process. A zero
// lastAPIPoll afterwards means no API reading exists at all, which counts as
// infinitely stale.
func (a *AnthropicAgent) seedLastAPIPoll() {
	if a.lastAPIPollRead {
		return
	}
	a.lastAPIPollRead = true
	if a.store == nil {
		return
	}
	at, ok, err := a.store.LastAnthropicAPISnapshot()
	if err != nil {
		a.logger.Warn("Could not read last Anthropic API snapshot time", "error", err)
		return
	}
	if ok {
		a.lastAPIPoll = at
	}
}

// noteAPIPoll records that the usage API has just been called.
func (a *AnthropicAgent) noteAPIPoll() {
	a.lastAPIPollRead = true
	a.lastAPIPoll = time.Now().UTC()
}

// SetAPIPollingDisabled controls whether the agent may call the OAuth usage API.
// Set true for "statusline only" mode so a stale or missing statusline file
// never silently falls through to the rate-limited usage API. Default (false)
// keeps API polling enabled for "auto" and "api" modes.
func (a *AnthropicAgent) SetAPIPollingDisabled(disabled bool) {
	a.apiPollingDisabled = disabled
}

// SetStatuslineStaleness sets the maximum age of the statusline file before
// falling back to API polling. Overrides the default (5 minutes).
func (a *AnthropicAgent) SetStatuslineStaleness(d time.Duration) {
	a.statuslineStaleness = d
}

// SetCCDetectionEnabled controls whether the agent checks if Claude Code is
// running before attempting OAuth token refresh. When disabled, OAuth refresh
// is always attempted regardless of CC state.
func (a *AnthropicAgent) SetCCDetectionEnabled(enabled bool) {
	if enabled {
		a.isClaudeCodeRunning = IsClaudeCodeRunning
	} else {
		a.isClaudeCodeRunning = func() bool { return false }
	}
}

// Run starts the Anthropic agent's polling loop. It polls immediately,
// then continues at the configured interval until the context is cancelled.
func (a *AnthropicAgent) Run(ctx context.Context) error {
	a.logger.Info("Anthropic agent started", "interval", a.interval)

	// Ensure any active session is closed on exit
	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Anthropic agent stopped")
	}()

	// Poll immediately on start
	a.poll(ctx)

	// Create ticker for periodic polling
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// Main polling loop
	for {
		select {
		case <-ticker.C:
			// Periodic statusline bridge health check (only when bridge is enabled)
			if a.statuslinePath != "" {
				EnsureStatuslineBridge(a.logger)
			}
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// isAuthError returns true if the error is an authentication/authorization error.
func isAuthError(err error) bool {
	return errors.Is(err, api.ErrAnthropicUnauthorized) || errors.Is(err, api.ErrAnthropicForbidden)
}

// rateLimitBackoff calculates the exponential backoff duration for OAuth 429 errors.
// Formula: min(base * 2^(n-1), max) where n is the failure count.
func rateLimitBackoff(failCount int) time.Duration {
	if failCount <= 0 {
		return rateLimitBaseBackoff
	}
	shift := failCount - 1
	if shift > 10 {
		shift = 10 // prevent overflow
	}
	backoff := rateLimitBaseBackoff * (1 << shift)
	if backoff > rateLimitMaxBackoff {
		return rateLimitMaxBackoff
	}
	return backoff
}

// autoRefreshAllowed reports whether OAuth refresh-and-write is enabled in settings.
func (a *AnthropicAgent) autoRefreshAllowed() bool {
	if a.store == nil {
		return true
	}
	return a.store.AutoRefreshTokensEnabled()
}

// refreshBlockReason names the guard preventing an OAuth refresh, or "" when
// one may proceed. It exists so the reason can be reported at the point a skip
// becomes visible - a paused account - rather than only in debug logs.
func (a *AnthropicAgent) refreshBlockReason() string {
	switch {
	case a.credsRefresh == nil:
		return "no credentials refresh configured for this account"
	case !a.autoRefreshAllowed():
		return "auto_refresh_tokens is disabled in settings"
	case a.rateLimitBackoffActive():
		return "OAuth refresh is in rate-limit backoff"
	default:
		return ""
	}
}

// refreshPauseCause explains why an OAuth refresh did not rescue this pause.
func (a *AnthropicAgent) refreshPauseCause() string {
	if a.lastRefreshBlockReason != "" {
		return a.lastRefreshBlockReason
	}
	return "the refresh was attempted and failed - see the preceding OAuth log"
}

// claudeCodeRunning reports whether the Claude Code CLI is executing, using the
// agent's override when set and the package-level detector otherwise.
func (a *AnthropicAgent) claudeCodeRunning() bool {
	if a.isClaudeCodeRunning != nil {
		return a.isClaudeCodeRunning()
	}
	return IsClaudeCodeRunning()
}

// pauseAuth stops polling after unrecoverable auth failures and schedules the
// first bounded self-recovery attempt.
func (a *AnthropicAgent) pauseAuth(failedToken string) {
	a.authPaused = true
	a.lastFailedToken = failedToken
	if a.authRetryAt.IsZero() {
		a.authRetryAt = time.Now().Add(authPausedRetryInterval)
	}
}

// resumeAuth clears all auth pause state after credentials are known good.
func (a *AnthropicAgent) resumeAuth() {
	a.authPaused = false
	a.authFailCount = 0
	a.lastFailedToken = ""
	a.authRetryAt = time.Time{}
	a.authRetryCount = 0
}

// noteRateLimited records one OAuth 429, advances the backoff, and escalates
// once when the run has persisted past rateLimitEscalateAfter.
//
// The escalation exists because backoff is only a pacing decision. A 429 that
// never clears - an edge block on a datacenter IP, say - is paced out to a 6h
// retry and otherwise looks identical to a healthy agent, which is how a refresh
// outage ran for days without an alert. Polling deliberately keeps retrying
// afterwards so the agent still self-heals if the block lifts.
//
// Returns the backoff applied, for the caller to log.
func (a *AnthropicAgent) noteRateLimited(err error) time.Duration {
	now := time.Now()
	a.rateLimitFailCount++
	if a.rateLimitFirstFailAt.IsZero() {
		a.rateLimitFirstFailAt = now
	}

	backoff := api.RetryAfter(err)
	if backoff <= 0 {
		backoff = rateLimitBackoff(a.rateLimitFailCount)
	}
	a.rateLimitPaused = true
	a.rateLimitResumeAt = now.Add(backoff)

	stuckFor := now.Sub(a.rateLimitFirstFailAt)
	if !a.rateLimitNotified && stuckFor >= rateLimitEscalateAfter {
		a.rateLimitNotified = true
		a.logger.Error("OAuth refresh rate limited persistently - alerting",
			"stuck_for", stuckFor.Round(time.Minute),
			"fail_count", a.rateLimitFailCount,
			"response_body", api.OAuthResponseBody(err),
			"action", "Check for an IP-level block, or re-authenticate to reset the credential")
		a.sendAuthErrorNotification(
			"Anthropic token refresh blocked",
			fmt.Sprintf("OAuth refresh has been rate limited for %s. Quota history is not being collected. "+
				"Re-authenticate with 'claude auth' if this persists.", stuckFor.Round(time.Minute)),
			true,
		)
	}
	return backoff
}

// clearRateLimitBackoff resets every field tracking the OAuth 429 run. Called
// wherever a refresh succeeds or the credentials change, so the next run starts
// from the base backoff and can alert again.
func (a *AnthropicAgent) clearRateLimitBackoff() {
	a.rateLimitFailCount = 0
	a.rateLimitPaused = false
	a.rateLimitResumeAt = time.Time{}
	a.rateLimitFirstFailAt = time.Time{}
	a.rateLimitNotified = false
}

// decayRateLimitBackoff walks the failure count back down after a successful
// poll, and ends the 429 run once it reaches zero.
//
// Only successful polls may call this. The backoff-expired retry path also
// decrements, but must leave the run timestamp alone: resetting it there would
// restart the escalation clock on every retry, so a permanent 429 would pace
// itself out and never alert - the behaviour this escalation exists to fix.
func (a *AnthropicAgent) decayRateLimitBackoff() {
	if a.rateLimitFailCount > 0 {
		a.rateLimitFailCount--
	}
	if a.rateLimitFailCount == 0 {
		a.rateLimitFirstFailAt = time.Time{}
		a.rateLimitNotified = false
	}
}

// rateLimitBackoffActive reports whether OAuth refresh is still in backoff.
// Once the window has elapsed it unpauses and decays the failure count by one,
// so a refresh that keeps failing holds its backoff level instead of
// escalating forever; a refresh that succeeds clears the count outright.
func (a *AnthropicAgent) rateLimitBackoffActive() bool {
	if !a.rateLimitPaused {
		return false
	}
	if time.Now().Before(a.rateLimitResumeAt) {
		return true
	}
	a.rateLimitPaused = false
	a.decayRateLimitBackoff()
	a.logger.Info("OAuth rate limit backoff expired, retrying refresh",
		"fail_count", a.rateLimitFailCount)
	return false
}

// authRetryBackoff returns the delay before the nth paused-state recovery attempt.
func authRetryBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return authPausedRetryInterval
	}
	shift := attempt - 1
	if shift > 10 {
		shift = 10 // prevent overflow
	}
	backoff := authPausedRetryInterval * (1 << shift)
	if backoff > authPausedRetryMaxInterval {
		return authPausedRetryMaxInterval
	}
	return backoff
}

// applyRefreshedTokens persists a rotated OAuth token pair and installs the new
// access token on the client.
//
// The tokens are applied in memory even when the write fails: the refresh token
// has already been consumed server-side, so the in-memory access token is the
// only usable credential at that point. The superseded on-disk access token is
// recorded so the pre-poll credential re-read does not immediately downgrade
// back to it - that downgrade would leave the agent polling with a dead token
// and a dead refresh token, an unrecoverable state (issue #111).
//
// Named accounts install a writer bound to their own credentials file; without
// it a background account would rotate the ambient account's tokens instead of
// its own. With ANTHROPIC_AUTH_ROOT set every account is a named account, so
// the ambient fallback would write to a path nothing reads.
func (a *AnthropicAgent) applyRefreshedTokens(newTokens *api.OAuthTokenResponse, supersededToken string) {
	// OAuth permits a successful response to omit refresh_token. In that case
	// the previously issued token remains current and must not be erased.
	if newTokens.RefreshToken == "" && a.credsRefresh != nil {
		if current := a.credsRefresh(); current != nil {
			newTokens.RefreshToken = current.RefreshToken
		}
	}
	writer := a.credsWrite
	if writer == nil {
		writer = api.WriteAnthropicCredentials
	}
	if err := writer(newTokens.AccessToken, newTokens.RefreshToken, newTokens.ExpiresIn); err != nil {
		a.logger.Error("Failed to save refreshed credentials", "error", err)
		a.supersededToken = supersededToken
	} else {
		a.supersededToken = ""
	}
	a.client.SetToken(newTokens.AccessToken)
	a.lastToken = newTokens.AccessToken
}

// rotateCredentials uses the account-scoped transaction when available, so the
// read/exchange/write runs under the shared credential lock. Without one it
// falls back to a bare exchange for agents that install no rotator.
func (a *AnthropicAgent) rotateCredentials(ctx context.Context, creds *api.AnthropicCredentials) (api.AnthropicRotation, error) {
	if a.credsRotate != nil {
		return a.credsRotate(ctx, creds.AccessToken)
	}
	tokens, err := api.RefreshAnthropicToken(ctx, creds.RefreshToken)
	if err != nil {
		return api.AnthropicRotation{}, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = creds.RefreshToken
	}
	return api.AnthropicRotation{Tokens: tokens}, nil
}

// acceptRotatedCredentials installs a rotation's pair. A rotation the
// transaction already stored needs no second write; anything else still has to
// be persisted here, because the exchange has already spent the old refresh
// token and an unwritten pair would be lost on restart.
func (a *AnthropicAgent) acceptRotatedCredentials(rot api.AnthropicRotation, oldAccess string) {
	if rot.PersistErr != nil {
		a.logger.Warn("Credential rotation could not be stored under the lock, retrying the write",
			"error", rot.PersistErr)
	}
	if rot.Persisted {
		a.client.SetToken(rot.Tokens.AccessToken)
		a.lastToken = rot.Tokens.AccessToken
		a.supersededToken = ""
		return
	}
	a.applyRefreshedTokens(rot.Tokens, oldAccess)
}

// noteOAuthRefreshFailure classifies a failed OAuth refresh and updates backoff
// state. source identifies the calling path for logs.
func (a *AnthropicAgent) noteOAuthRefreshFailure(err error, source string) {
	switch {
	case errors.Is(err, api.ErrOAuthRateLimited):
		backoff := a.noteRateLimited(err)
		a.logger.Warn("OAuth refresh rate limited - backing off",
			"source", source,
			"fail_count", a.rateLimitFailCount,
			"backoff", backoff,
			"resume_at", a.rateLimitResumeAt,
			"response_body", api.OAuthResponseBody(err))
	case errors.Is(err, api.ErrOAuthInvalidGrant):
		a.authFailCount = maxAuthFailures
		a.pauseAuth(a.lastToken)
		a.logger.Error("OAuth refresh token invalid (invalid_grant) - polling PAUSED",
			"source", source,
			"error", err,
			"action", "Re-authenticate with 'claude auth' to resume polling")
		a.sendAuthErrorNotification(
			"OAuth refresh token expired",
			"Refresh token is invalid or revoked. Re-authenticate with 'claude auth' to resume polling.",
			false,
		)
	default:
		a.logger.Error("OAuth refresh failed", "source", source, "error", err)
	}
}

// tryOAuthRecovery attempts a single OAuth refresh to recover from repeated
// 401/403 responses, where the stored access token has simply expired and no
// Claude Code session is around to rotate it (issue #111).
//
// Auto-refresh must be enabled and the OAuth backoff must not be active. A
// resident Claude Code is NOT a hard block here (unlike proactiveRefresh): the
// stored token has already been rejected, so deferring to it only guarantees a
// dead account. Every skip is logged with its reason - a silent one strands the
// account with nothing in the log to explain it.
// Returns true when new credentials were obtained and installed on the client.
func (a *AnthropicAgent) tryOAuthRecovery(ctx context.Context, source string) bool {
	if reason := a.refreshBlockReason(); reason != "" {
		// Warn, not Debug: reaching here means polling is already failing or
		// paused, so a silent skip leaves an operator with a dead account and
		// no stated cause - which is exactly how this went undiagnosed.
		a.logger.Warn("Auth-failure OAuth refresh skipped",
			"source", source,
			"reason", reason,
			"resume_at", a.rateLimitResumeAt)
		a.lastRefreshBlockReason = reason
		return false
	}
	a.lastRefreshBlockReason = ""

	creds := a.credsRefresh()
	if creds == nil || creds.RefreshToken == "" {
		a.logger.Warn("Auth-failure OAuth refresh unavailable - no refresh token", "source", source)
		a.lastRefreshBlockReason = "no refresh token in the credential store"
		return false
	}

	// Claude Code holding the same credential is a reason to defer a *proactive*
	// refresh, not this one: the stored token has already been rejected, so
	// there is no live session token left to protect. Prefer whatever Claude
	// Code has written since the failures started, and only exchange when the
	// store really is still stale. The rotation lock makes that safe.
	if a.claudeCodeRunning() {
		if creds.AccessToken != "" && creds.AccessToken != a.lastFailedToken && creds.AccessToken != a.lastToken {
			a.logger.Info("Claude Code is running and has rotated credentials - adopting them", "source", source)
			a.client.SetToken(creds.AccessToken)
			a.lastToken = creds.AccessToken
			a.supersededToken = ""
			return true
		}
		a.logger.Warn("Claude Code is running but the stored credentials are still the rejected ones - refreshing under the credential lock",
			"source", source)
	}

	a.logger.Info("Attempting OAuth refresh to recover from auth failures", "source", source)
	rotation, err := a.rotateCredentials(ctx, creds)
	if err != nil {
		a.noteOAuthRefreshFailure(err, source)
		return false
	}

	// Refresh succeeded - reset OAuth backoff state
	a.clearRateLimitBackoff()

	// CRITICAL: refresh tokens are one-time use, persist the new pair immediately.
	a.acceptRotatedCredentials(rotation, creds.AccessToken)
	if rotation.Adopted {
		a.logger.Info("Adopted credentials rotated by another process", "source", source)
	}
	a.logger.Info("OAuth token refreshed after auth failures",
		"source", source,
		"expires_in_hours", rotation.Tokens.ExpiresIn/3600)
	return true
}

// tryPausedAuthRecovery makes a bounded attempt to leave the auth-paused state.
// Without it the only exit is Claude Code independently rewriting credentials,
// which can leave polling dead for hours or days (issue #111). Returns true when
// polling may resume this cycle.
func (a *AnthropicAgent) tryPausedAuthRecovery(ctx context.Context) bool {
	now := time.Now()
	if a.authRetryAt.IsZero() || now.Before(a.authRetryAt) {
		return false
	}

	a.authRetryCount++
	a.authRetryAt = now.Add(authRetryBackoff(a.authRetryCount))
	a.logger.Info("Attempting recovery from paused Anthropic polling",
		"attempt", a.authRetryCount,
		"next_retry_at", a.authRetryAt)

	if !a.tryOAuthRecovery(ctx, "auth pause retry") {
		return false
	}

	// Keep the escalating schedule until a poll actually succeeds - the fresh
	// token may still be rejected, and retries must not restart at 15m forever.
	nextRetry, attempts := a.authRetryAt, a.authRetryCount
	a.resumeAuth()
	a.authRetryAt, a.authRetryCount = nextRetry, attempts
	a.logger.Info("Auth failure pause lifted - token refreshed via OAuth")
	return true
}

// proactiveRefresh attempts to refresh the OAuth token before it expires.
// Respects rate limit backoff to avoid burning refresh tokens.
// Skips proactive refresh if Claude Code is running to avoid competing for the
// same refresh token (onWatch refreshes would invalidate Claude Code's pending
// refresh and cause re-authentication).
func (a *AnthropicAgent) proactiveRefresh(ctx context.Context, creds *api.AnthropicCredentials) {
	if !a.autoRefreshAllowed() {
		a.logger.Debug("Skipping proactive OAuth refresh - auto_refresh_tokens disabled")
		return
	}
	// Skip if these are credentials we already refreshed away from but could
	// not persist. They still look expiring on disk, so without this guard the
	// agent would mint - and burn - a new one-time-use refresh token on every
	// single poll, which invalidates Claude Code's session.
	if creds.AccessToken != "" && creds.AccessToken == a.supersededToken {
		a.logger.Debug("Skipping proactive OAuth refresh - stored credentials already superseded by an unsaved refresh")
		return
	}
	// Skip if Claude Code is running - avoid competing for the same refresh token.
	// onWatch refreshing burns Claude Code's scheduled refresh, causing invalid_grant.
	if a.claudeCodeRunning() {
		a.logger.Debug("Skipping proactive OAuth refresh - Claude Code is running",
			"expires_in", creds.ExpiresIn.Round(time.Second))
		return
	}

	// Skip if in rate limit backoff
	if a.rateLimitBackoffActive() {
		a.logger.Debug("Skipping proactive OAuth refresh - in rate limit backoff",
			"resume_at", a.rateLimitResumeAt)
		return
	}

	a.logger.Info("Token expiring soon, attempting proactive OAuth refresh",
		"expires_in", creds.ExpiresIn.Round(time.Second))

	rotation, err := a.rotateCredentials(ctx, creds)
	if err != nil {
		if errors.Is(err, api.ErrOAuthRateLimited) {
			backoff := a.noteRateLimited(err)
			a.logger.Warn("Proactive OAuth refresh rate limited - backing off",
				"fail_count", a.rateLimitFailCount,
				"backoff", backoff,
				"server_retry_after", api.RetryAfter(err),
				"response_body", api.OAuthResponseBody(err))
		} else if errors.Is(err, api.ErrOAuthInvalidGrant) {
			a.authFailCount = maxAuthFailures
			a.pauseAuth(a.lastToken)
			a.logger.Error("Proactive OAuth refresh - invalid_grant, polling PAUSED",
				"error", err,
				"action", "Re-authenticate with 'claude auth' to resume polling")
		} else {
			a.logger.Error("Proactive OAuth refresh failed", "error", err)
		}
		return
	}

	// Proactive refresh succeeded - reset all backoff state
	a.clearRateLimitBackoff()

	// CRITICAL: Save new tokens to disk IMMEDIATELY
	a.acceptRotatedCredentials(rotation, creds.AccessToken)
	if rotation.Adopted {
		a.logger.Info("Adopted credentials rotated by another process", "source", "proactive refresh")
	}
	a.logger.Info("Proactively refreshed OAuth token",
		"expires_in_hours", rotation.Tokens.ExpiresIn/3600)

	// Reset auth failures since we have fresh credentials
	if a.authPaused {
		a.resumeAuth()
		a.logger.Info("Auth failure pause lifted - token refreshed via OAuth")
	}
}

// poll performs a single Anthropic poll cycle: fetch quotas, store snapshot, process with tracker.
func (a *AnthropicAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	// Statusline bridge: try to read rate limit data from Claude Code's statusline
	// output file. If fresh and valid, use it and skip the rate-limited OAuth usage API.
	// Falls back to API polling if data is stale, missing, corrupt, or out of range.
	if a.statuslinePath != "" && isStatuslineFresh(a.statuslinePath, a.statuslineStaleness) {
		rl, err := readStatuslineData(a.statuslinePath)
		if err != nil {
			a.logger.Info("Statusline read error, falling back to API polling", "error", err)
		} else if !isValidStatuslineData(rl) {
			a.logger.Warn("Statusline data invalid, falling back to API polling")
		} else {
			now := time.Now().UTC()
			snapshot := statuslineToSnapshot(rl, now)
			snapshot.AccountID = a.accountID
			if _, err := a.store.InsertAnthropicSnapshot(snapshot); err != nil {
				a.logger.Error("Failed to insert statusline snapshot", "error", err)
				return // don't fall through to API polling on DB error
			}
			if a.tracker != nil {
				if err := a.tracker.Process(snapshot); err != nil {
					a.logger.Error("Anthropic tracker processing failed", "error", err)
				}
			}
			a.pollCycleCount++
			a.logger.Info("Anthropic poll complete",
				"source", "statusline",
				"quota_count", len(snapshot.Quotas),
				"cycle", a.pollCycleCount)
			a.decayRateLimitBackoff()
			// Hybrid: periodically do a full API poll for supplementary quotas
			// (seven_day_sonnet, extra_usage, etc.) that statusline doesn't provide.
			if a.beginSupplementalAPIPoll() {
				a.logger.Info("Hybrid API poll triggered",
					"cycle", a.pollCycleCount,
					"interval", a.apiPollInterval())
				// Fall through to the API polling path below
			} else {
				return // Statusline only - skip API polling this cycle
			}
		}
	}

	// "Statusline only" mode: never touch the OAuth usage API, even when the
	// statusline file is stale or missing. Everything below is the API path,
	// which serves only "auto" (hybrid) and "api" modes. Without this guard a
	// stale or absent statusline silently falls through to FetchQuotas, breaking
	// the documented contract of statusline-only mode - common in headless usage
	// (SDK/--print/remote-control) where no interactive TUI refreshes the file.
	if a.apiPollingDisabled {
		return
	}

	// Proactive OAuth refresh. Skipped while paused: an expired token is always
	// "expiring soon", so this would retry on every single poll. Recovery from
	// the paused state is owned by tryPausedAuthRecovery, which is bounded.
	if a.credsRefresh != nil && !a.authPaused {
		if creds := a.credsRefresh(); creds != nil {
			// Check if token is expiring soon or already expired
			if creds.IsExpiringSoon(tokenRefreshThreshold) && creds.RefreshToken != "" {
				a.proactiveRefresh(ctx, creds)
			}
		}
	}

	// Refresh token before each poll (picks up rotated credentials from disk)
	var newToken string
	if a.tokenRefresh != nil {
		newToken = a.tokenRefresh()
		if newToken != "" && newToken == a.supersededToken {
			// Stored credentials are behind an OAuth refresh we could not
			// persist - keep the in-memory token rather than downgrading.
			a.logger.Debug("Ignoring stored credentials superseded by an unsaved OAuth refresh")
			newToken = a.lastToken
		}
		if newToken != "" && newToken != a.lastToken {
			a.client.SetToken(newToken)
			a.lastToken = newToken
			a.logger.Info("Anthropic token refreshed from credentials")

			// If we were paused due to auth failures and credentials changed, resume
			if a.authPaused && newToken != a.lastFailedToken {
				a.resumeAuth()
				a.logger.Info("Auth failure pause lifted - new credentials detected")
			}

			// If we were in rate limit backoff and credentials changed, resume
			if a.rateLimitPaused {
				a.clearRateLimitBackoff()
				a.logger.Info("Rate limit backoff lifted - new credentials detected")
			}
		}
	}

	// If auth is paused, skip polling until credentials change or a bounded
	// OAuth recovery attempt succeeds.
	if a.authPaused && !a.tryPausedAuthRecovery(ctx) {
		return
	}

	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		// A usage-API 429 is not an authentication failure. Rotating a one-time
		// refresh token to evade this limit races every Claude Code process sharing
		// the credential and can force a global re-login. Respect the limit and try
		// again on a later scheduled poll instead.
		if errors.Is(err, api.ErrAnthropicRateLimited) {
			a.logger.Warn("Anthropic usage API rate limited; preserving OAuth credentials and retrying on the next poll")
			return
		}

		// On auth error (401 or 403), force token re-read and retry once
		if isAuthError(err) && a.tokenRefresh != nil {
			a.logger.Warn("Anthropic auth error, forcing credential re-read", "error", err)
			a.lastToken = "" // force re-read even if token hasn't changed on disk
			if retryToken := a.tokenRefresh(); retryToken != "" {
				a.client.SetToken(retryToken)
				a.lastToken = retryToken
				a.logger.Info("Retrying with refreshed token")
				resp, err = a.client.FetchQuotas(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					// Retry also failed - count this as an auth failure
					if isAuthError(err) {
						a.authFailCount++
						a.logger.Error("Anthropic auth retry failed",
							"error", err,
							"failure_count", a.authFailCount,
							"max_failures", maxAuthFailures)

						if a.authFailCount >= maxAuthFailures {
							// Before pausing, try one OAuth refresh: the stored
							// access token may simply have expired with no Claude
							// Code session around to rotate it (issue #111).
							// Re-reading the same expired credentials, which is
							// all tokenRefresh does, can never fix that.
							if a.tryOAuthRecovery(ctx, "auth failure") {
								resp, err = a.client.FetchQuotas(ctx)
								if err == nil {
									a.authFailCount = 0
									a.lastFailedToken = ""
									a.logger.Info("Auth recovered via OAuth refresh")
									goto processResponse
								}
								if ctx.Err() != nil {
									return
								}
								a.logger.Error("Poll after OAuth recovery refresh failed", "error", err)
							}
							if a.authPaused {
								// Already paused by the refresh attempt (invalid_grant)
								return
							}
							a.pauseAuth(retryToken)
							a.logger.Error("Anthropic polling PAUSED due to repeated auth failures",
								"failure_count", a.authFailCount,
								"oauth_refresh_skipped", a.refreshPauseCause(),
								"action", "Re-authenticate with 'claude auth' to resume polling")
							a.sendAuthErrorNotification(
								"Anthropic polling paused",
								fmt.Sprintf("Repeated auth failures (%d). Re-authenticate with 'claude auth' to resume.", a.authFailCount),
								false,
							)
						}
					} else {
						a.logger.Error("Anthropic retry failed with non-auth error", "error", err)
					}
					return
				}
				// Retry succeeded - reset auth failure count and fall through
				a.authFailCount = 0
			} else {
				a.logger.Error("No Anthropic token available after re-read")
				return
			}
		} else {
			a.logger.Error("Failed to fetch Anthropic quotas", "error", err)
			return
		}
	} else {
		// Success - decay rate limit backoff. Auth state is reset below, at
		// processResponse, so the recovery paths that jump straight there
		// clear it too.
		a.decayRateLimitBackoff()
	}

processResponse:
	// Reaching here means a poll succeeded, whether directly or via one of the
	// recovery paths that goto here. Clear all auth failure and retry
	// scheduling so a later pause starts from a fresh backoff rather than
	// inheriting a stale, already-elapsed deadline.
	a.authFailCount = 0
	a.authRetryCount = 0
	a.authRetryAt = time.Time{}

	// Convert to snapshot and store
	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)
	snapshot.AccountID = a.accountID

	if _, err := a.store.InsertAnthropicSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Anthropic snapshot", "error", err)
		return
	}

	// Process with tracker (log error but don't stop)
	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Anthropic tracker processing failed", "error", err)
		}
	}

	// Check notification thresholds
	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "anthropic",
				QuotaKey:    q.Name,
				AccountID:   notifyAccountID(a.accountID),
				Utilization: q.Utilization,
			})
		}
	}

	// Report to session manager - extract utilization values for change detection.
	// Use fixed order matching UI columns: five_hour, seven_day, seven_day_sonnet
	// (alphabetical sort would put monthly_limit between them, breaking the mapping).
	if a.sm != nil {
		// Build a map for O(1) lookup
		quotaMap := make(map[string]float64, len(snapshot.Quotas))
		for _, q := range snapshot.Quotas {
			quotaMap[q.Name] = q.Utilization
		}
		// Report in fixed order matching session columns (sub, search, tool)
		values := []float64{
			quotaMap["five_hour"],        // Column 0: 5-Hour %
			quotaMap["seven_day"],        // Column 1: Weekly %
			quotaMap["seven_day_sonnet"], // Column 2: Sonnet %
		}
		a.sm.ReportPoll(values)
	}

	// Log poll completion
	quotaCount := len(snapshot.Quotas)
	var maxUtil float64
	for _, q := range snapshot.Quotas {
		if q.Utilization > maxUtil {
			maxUtil = q.Utilization
		}
	}

	// Any completed API poll resets the supplementary schedule, including one
	// reached because the statusline was stale rather than because the hybrid
	// slot came due.
	a.noteAPIPoll()

	a.logger.Info("Anthropic poll complete",
		"source", "api",
		"account", a.accountName,
		"account_id", a.accountID,
		"quota_count", quotaCount,
		"max_utilization", maxUtil,
	)
}
