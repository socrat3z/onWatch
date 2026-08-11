---
id: TASK-17
title: Tolerate a missing Anthropic auth root
status: Done
assignee: []
created_date: '2026-08-10 19:23'
updated_date: '2026-08-11 00:00'
labels:
  - review-multi-account
  - rev-p3-5
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
ordinal: 17000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: Architect | Verdict: Quick win

What & where: Make AnthropicSource.List tolerate a missing root the way ListDirectories does - ListDirectories returns (nil, nil) when the root is absent (internal/account/account.go:47-49) but List then calls EvalSymlinks unconditionally and propagates the error (internal/account/anthropic_source.go:24-27). AntigravitySource.List does not have this problem.

Why: Review section 4 finding L16 - an unmounted volume produces a Warn log every 60 seconds forever.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.5
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A configured-but-absent ANTHROPIC_AUTH_ROOT yields zero definitions and no repeated warning
- [x] #2 A present root with a symlinked entry still resolves and is still containment-checked
<!-- AC:END -->
