package agent

// backoffBaseCycles is the number of poll cycles skipped after the first
// consecutive rate-limit failure.
const backoffBaseCycles = 1

// backoffMaxCycles is the maximum number of poll cycles ever skipped,
// regardless of how many consecutive failures have occurred.
const backoffMaxCycles = 10

// pollBackoff tracks a growing, capped number of poll cycles to skip after a
// provider reports a rate-limit error. It mirrors rateLimitBackoff's
// exponential-growth-then-cap shape, but counts poll cycles instead of wall
// time - providers with an unused RateLimited sentinel don't need Anthropic's
// OAuth-refresh-bypass complexity, just "back off the fixed poll cadence".
type pollBackoff struct {
	failCount     int
	skipRemaining int
}

// RateLimited records a rate-limit failure and arms the backoff to skip the
// next N poll cycles, where N grows with consecutive failures up to
// backoffMaxCycles. Returns the number of cycles armed.
func (b *pollBackoff) RateLimited() int {
	b.failCount++
	b.skipRemaining = backoffCycles(b.failCount)
	return b.skipRemaining
}

// backoffCycles computes cycles-to-skip for a given consecutive failure
// count. Formula: min(base * 2^(n-1), max).
func backoffCycles(failCount int) int {
	if failCount <= 0 {
		return backoffBaseCycles
	}
	shift := failCount - 1
	if shift > 20 {
		shift = 20 // prevent overflow
	}
	cycles := backoffBaseCycles << shift
	if cycles > backoffMaxCycles {
		return backoffMaxCycles
	}
	return cycles
}

// ShouldSkip reports whether the current poll cycle should be skipped due to
// an active backoff, consuming one cycle of the remaining skip count.
func (b *pollBackoff) ShouldSkip() bool {
	if b.skipRemaining <= 0 {
		return false
	}
	b.skipRemaining--
	return true
}

// Reset clears all backoff state after a successful poll.
func (b *pollBackoff) Reset() {
	b.failCount = 0
	b.skipRemaining = 0
}
