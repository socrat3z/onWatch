---
id: TASK-1
title: Scope QueryLatest* to the provider default account
status: Done
assignee: []
created_date: '2026-08-10 19:21'
updated_date: '2026-08-10 20:09'
labels:
  - review-multi-account
  - rev-p1-1
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: high
ordinal: 1000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S1 | Lens: Architect, PO, End-user | Verdict: Do

What & where: Make QueryLatestAnthropic / QueryLatestAntigravity route through scopedProviderAccountID like every other store method - internal/store/anthropic_store.go:77-89 and internal/store/antigravity_store.go:100. Then give currentAnthropic the same always-resolve-default shape currentAntigravity already has (internal/web/handlers.go:5716-5724), and pass an account to the six unscoped call sites: handlers.go 4313, 5851, 6089, 6136, 8680, 8866.

Why: Review section 2.2 finding L1 - the default view returns whichever account polled last, so a two-account install silently alternates between two peoples quota numbers.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P1.1
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 QueryLatestAnthropic() with no argument returns the providers default account row, not the global maximum captured_at
- [x] #2 A store test with two accounts, where the non-default account has the newer snapshot, asserts the no-argument call returns the default accounts snapshot
- [x] #3 No caller of QueryLatest* in internal/web/ omits an account argument
- [x] #4 GET /api/current?provider=anthropic and ?provider=antigravity resolve the default account identically
<!-- AC:END -->
