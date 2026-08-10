---
id: TASK-10
title: Replace prompt and alert rename flow with an in-app dialog
status: To Do
assignee: []
created_date: '2026-08-10 19:22'
labels:
  - review-multi-account
  - rev-p2-5
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
ordinal: 10000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: Designer, End-user | Verdict: Quick win

What & where: Replace window.prompt / window.alert in the rename flow (internal/web/static/app.js:676-687) with the dashboards own dialog, and show the servers actual error - UpdateProviderAccountAlias already returns a precise message (internal/store/provider_account_helpers.go:26-29) that the current catch discards.

Why: Review section 5 - unstyled browser modals plus a dead-end generic error that replaces the servers precise 1-64 character message with a guess.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.5
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Renaming uses an in-app dialog consistent with other dashboard dialogs
- [ ] #2 A rejected alias displays the servers message verbatim
- [ ] #3 The 1-64 character rule is stated in the dialog before submission
<!-- AC:END -->
