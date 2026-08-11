---
id: TASK-7
title: Add account context to the combined dashboard
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-11 00:00'
updated_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p2-2
dependencies:
  - TASK-1
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 7000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Designer, PO, End-user | Verdict: Do | needs: P1.1

What & where: Give the combined (both) dashboard account context. getCurrentProvider() returns both (internal/web/static/app.js:48-50), so providerAccountParam never fires and the picker never renders. Either label each Anthropic/Antigravity card with the account it is showing, or render one card per active account.

Why: Review sections 5 and 6 step 7 - the highest-traffic screen is the one with no account concept, and combined with finding L1 the numbers shown are non-deterministic.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.2
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every Anthropic and Antigravity card in the combined view names the account it represents
- [x] #2 The account shown is deterministic across refreshes
- [x] #3 A single-account install sees no new chrome
<!-- AC:END -->
