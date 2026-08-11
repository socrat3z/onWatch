---
id: TASK-20
title: Normalize formatting in new multi-account code
status: Done
assignee: []
created_date: '2026-08-10 19:23'
updated_date: '2026-08-11 00:00'
labels:
  - review-multi-account
  - rev-p3-8
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
ordinal: 20000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: Architect | Verdict: Quick win

What & where: Run gofmt and app.sh --smoke over the changeset and normalize the single-line if/select bodies introduced in internal/api/anthropic_token.go:99 and internal/api/antigravity_cli.go:143. While there, drop the unreachable os.MkdirAll at anthropic_token.go:117 - line 107 already returned if the file could not be read.

Why: Review section 4 - style drift from surrounding code and a possible CI formatting failure. NOTE: gofmt could not be run during the review (mise reported no go/gofmt shim), so verify before assuming a violation exists.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.8
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 gofmt -l reports nothing for the changed files
- [ ] #2 ./app.sh --smoke passes
<!-- AC:END -->
