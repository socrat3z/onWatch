---
id: TASK-18
title: Log and document silent config overrides
status: To Do
assignee: []
created_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p3-6
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: low
ordinal: 18000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S3 | Lens: PO | Verdict: Quick win

What & where: Log and document the silent overrides: ANTIGRAVITY_SOURCE is ignored in manager mode (hardcoded at internal/agent/antigravity_agent_manager.go:90) and ANTHROPIC_TOKEN stops being used when ANTHROPIC_AUTH_ROOT is set (main.go:789).

Why: Review section 3 - configuration the user explicitly set is discarded with no signal in logs or docs.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.6
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each override emits one startup Warn naming both the ignored setting and the reason
- [ ] #2 docs/WITH_USER_ENV.md documents both interactions
<!-- AC:END -->
