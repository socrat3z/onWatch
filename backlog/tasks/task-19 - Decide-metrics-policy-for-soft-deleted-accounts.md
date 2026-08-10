---
id: TASK-19
title: Decide metrics policy for soft-deleted accounts
status: To Do
assignee: []
created_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p3-7
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
ordinal: 19000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: Architect | Verdict: Do

What & where: Decide explicitly whether Prometheus should keep emitting for soft-deleted accounts. scrapeAnthropic/scrapeAntigravity iterate QueryProviderAccounts (includes deleted) at internal/metrics/metrics.go:316 and :498; QueryActiveProviderAccounts exists.

Why: Review section 4 finding L10 - unbounded label cardinality growth. But deleted accounts do retain queryable history, so this is a genuine trade-off rather than an obvious bug and deserves a recorded decision.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.7
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The choice is made and recorded in a comment at both call sites
- [ ] #2 If deleted accounts are excluded, a test asserts their series stop
<!-- AC:END -->
