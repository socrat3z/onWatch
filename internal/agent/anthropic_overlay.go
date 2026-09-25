package agent

import (
	"context"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// CredentialsWriteFunc persists OAuth rotation for this agent's exact account.
type CredentialsWriteFunc func(accessToken, refreshToken string, expiresIn int) error

// CredentialsRotateFunc installs an account-scoped, cross-process-safe OAuth transaction.
type CredentialsRotateFunc func(context.Context, string) (api.AnthropicRotation, error)

// SetAccountContext assigns every stored snapshot and log entry to one
// provider-account record. The ambient agent retains the zero/default value.
func (a *AnthropicAgent) SetAccountContext(accountID int64, accountName string) {
	a.accountID = accountID
	a.accountName = accountName
}

// SetCredentialsWriter persists OAuth rotation for this agent's exact account.
func (a *AnthropicAgent) SetCredentialsWriter(fn CredentialsWriteFunc) {
	a.credsWrite = fn
}

// SetCredentialsRotator installs an account-scoped, cross-process-safe OAuth
// transaction. The string argument is the access-token generation the caller
// observed; the bool result reports that a newer stored generation was adopted.
func (a *AnthropicAgent) SetCredentialsRotator(fn CredentialsRotateFunc) {
	a.credsRotate = fn
}
