---
id: TASK-9
title: Show the account picker at one account
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-11 00:00'
labels:
  - review-multi-account
  - rev-p2-4
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
ordinal: 9000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: Designer | Verdict: Quick win

What & where: Render the account identity at one account instead of hiding the control - replace the accounts.length <= 1 early return (internal/web/static/app.js:655) with a non-interactive label that still exposes Edit display alias.

Why: Review sections 5 and 6 step 4 - a users first named account is invisible and unrenameable until a second one exists. Resolves the PO/Designer tension recorded in the section 5 callout.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.4
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 With exactly one account, the header shows that accounts alias
- [x] #2 The alias-edit action is reachable at one account
- [x] #3 With zero named accounts nothing new renders
<!-- AC:END -->
