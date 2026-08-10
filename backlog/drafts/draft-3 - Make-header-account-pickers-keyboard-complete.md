---
id: DRAFT-3
title: Make header account pickers keyboard-complete
status: Draft
assignee: []
created_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p4-3
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: Designer | Verdict: DEFER until an accessibility audit, or the first keyboard-only user report

What & where: Make all three header pickers (Codex, MiniMax, provider-account) keyboard-complete - roving tabindex, arrow/Home/End handling, aria-activedescendant, focus restoration on close. The new picker inherits this debt from the Codex one (internal/web/static/app.js:647-714), so fix them together.

Why: Review section 5 - role=listbox promises keyboard semantics the implementation does not provide, which is worse for screen-reader users than no role at all.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P4.3 (deferred - kept as a draft, not open work)
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each picker is fully operable by keyboard alone
- [ ] #2 Focus returns to the trigger on close
- [ ] #3 The alias-edit action is not exposed as a listbox option
<!-- AC:END -->
