---
id: DRAFT-2
title: Coordinate credential write-back with provider CLIs
status: Draft
assignee: []
created_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p4-2
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: DEFER until the first report of an account losing auth after a token refresh

What & where: WriteAnthropicCredentialsFile (internal/api/anthropic_token.go:104-120) does read-modify-rename with no lock or compare-and-swap; refresh tokens are single-use per CLAUDE.md. Add an advisory lock or a CAS on the existing refreshToken value, and fsync before rename.

Why: Review section 2.4 finding L17 - a concurrent refresh by Claude Code makes onWatch reinstate a spent token and kill that accounts auth. Pre-existing, but per-account isolation multiplies the number of files racing.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P4.2 (deferred - kept as a draft, not open work)
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A concurrent-writer test cannot produce a credentials file containing a superseded refresh token
- [ ] #2 The file is durable across a simulated crash between write and rename
<!-- AC:END -->
