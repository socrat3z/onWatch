---
id: TASK-21
title: Wire rate-limit backoff into providers with unused RateLimited sentinels
status: To Do
assignee: []
created_date: '2026-08-12 09:23'
labels: []
dependencies: []
ordinal: 21000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
DeepSeek, Kimi, Moonshot, and OpenRouter API clients already classify HTTP 429 into a distinct sentinel error (ErrDeepSeekRateLimited, ErrKimiRateLimited, ErrMoonshotRateLimited, ErrOpenRouterRateLimited) but none of their agents branch on it - it falls through to the generic "log and return" path and gets retried at the same fixed poll cadence forever, with zero differentiation from a transient network blip.

This is the same failure mode Anthropic's usage-check endpoint already hit in production (see anthropic_agent.go rateLimitBackoff, issue #16): a quota-check endpoint turning out to have tighter rate limits than assumed, hammered repeatedly at a fixed interval. Anthropic's fix was exponential backoff scoped to 429 specifically. The other four providers have the detection already built and wired to nothing.

Scope: add a small shared backoff helper (skip N poll cycles on RateLimited error, growing then capped - doesn't need Anthropic's full OAuth-refresh-bypass complexity) and wire it into deepseek_agent.go, kimi_agent.go, moonshot_agent.go, and openrouter_agent.go where they check FetchQuotas errors.

Not in scope: a generic backoff framework across all 15 providers - only the four with an already-defined RateLimited sentinel that's currently unused.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Shared backoff helper exists and is unit tested (mirrors rateLimitBackoff style: growing delay, capped max)
- [ ] #2 DeepSeek, Kimi, Moonshot, and OpenRouter agents skip polling for the backoff window when FetchQuotas returns their RateLimited sentinel, instead of retrying every fixed interval
- [ ] #3 Backoff resets after a successful poll
- [ ] #4 go test -race ./... and go vet ./... pass
<!-- AC:END -->
