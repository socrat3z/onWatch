---
id: TASK-15
title: Unexport account-less store queries
status: Done
assignee: []
created_date: '2026-08-10 19:23'
updated_date: '2026-08-11 00:00'
updated_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p3-3
dependencies:
  - TASK-1
  - TASK-2
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 15000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: Do | after: P1.1, P1.2

What & where: Make L1-class bugs unrepresentable: unexport the unscoped QueryAnthropicRange / QueryAntigravityRange / QueryAnthropicCycleHistory / QueryAntigravityCycleHistory, or give them a required account parameter, so internal/web/ cannot reach an account-less query.

Why: Review section 4 - the store currently exports scoped and unscoped variants of the same query and trusts callers to choose correctly. That trust is what produced findings L1 and L2.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.3
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 No exported store method for these four providers can execute without an account predicate
- [x] #2 internal/web/ compiles against the scoped API only
<!-- AC:END -->
