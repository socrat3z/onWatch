---
id: TASK-5
title: Allowlist the agy child process environment
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-10 21:49'
labels:
  - review-multi-account
  - rev-p1-5
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 5000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: Quick win

What & where: Stop leaking the daemons secrets into the agy child. In NewAntigravityCLIRunnerWithEnv (internal/api/antigravity_cli.go:78-84), build the child environment from an explicit allowlist (PATH, TERM, HOME, XDG_RUNTIME_DIR, AGY_CLI_DISABLE_AUTO_UPDATE, locale) instead of os.Environ(), matching what the doc comment already promises.

Why: Review section 4 - the comment says only non-secret process environment values; the code passes every provider API key from .env to a third-party binary. Same trust boundary the prior with-user-env review flagged as its finding 1.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P1.5
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The agy child process environment contains no *_API_KEY, *_TOKEN or ONWATCH_* value
- [x] #2 A test asserts the constructed env against an expected allowlist
- [x] #3 agy still authenticates and returns a snapshot with the accounts HOME
<!-- AC:END -->
