package agent

import "log/slog"

// rateLimited arms provider backoff and publishes the failed attempt before
// the caller returns. Deliberately skipped cycles retain this health reason.
func (b *pollBackoff) rateLimited(provider string, err error, logger *slog.Logger) {
	cycles := b.RateLimited()
	logger.Warn("provider rate limited, backing off", "provider", provider, "skip_cycles", cycles)
	pollFailed(provider, "", err)
}
