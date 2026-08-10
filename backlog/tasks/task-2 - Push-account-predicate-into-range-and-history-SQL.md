---
id: TASK-2
title: Push account predicate into range and history SQL
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-10 20:09'
labels:
  - review-multi-account
  - rev-p1-2
dependencies:
  - TASK-1
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: high
ordinal: 2000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S1 | Lens: Architect | Verdict: Do | after: P1.2 follows P1.1

What & where: Push the account predicate into the SQL of the four filter-after-limit wrappers instead of post-filtering with an N+1 lookup: QueryAnthropicRangeForAccount (internal/store/anthropic_store.go:130-148), QueryAnthropicCycleHistoryForAccount (anthropic_store.go:386-401), QueryAntigravityRangeForAccount (internal/store/antigravity_store.go:263-283), QueryAntigravityCycleHistoryForAccount (antigravity_store.go:424). Model them on QueryAnthropicUtilizationSeriesForAccount (anthropic_store.go:151-173), which already does this correctly.

Why: Review section 2.2 finding L2 - LIMIT is spent across all accounts so charts silently truncate, and one extra query per row breaks the bounded-query guardrail.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P1.2
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each of the four functions issues exactly one query
- [x] #2 A test with two accounts each holding 300 snapshots, requesting limit=200 for one account, returns 200 rows for that account
- [x] #3 No SELECT account_id FROM ..._snapshots WHERE id = ? remains in internal/store/
<!-- AC:END -->
