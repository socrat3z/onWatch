---
id: TASK-12
title: Warn on Codex native and legacy profile name collision
status: To Do
assignee: []
created_date: '2026-08-10 19:22'
labels:
  - review-multi-account
  - rev-p2-7
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 12000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: Do

What & where: In loadAndStartProfileValue (internal/agent/codex_agent_manager.go:292-298), log when a native account is skipped because a legacy profile of the same name is already running, and say which one won. Native accounts are always loaded after legacy profiles (codex_agent_manager.go:216).

Why: Review section 4 finding L9 - a container codex-login for an alias that collides with a legacy profile appears to succeed but never polls, with no warning.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.7
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A colliding native account produces a Warn naming the alias and the winning source
- [ ] #2 Non-colliding native accounts start unchanged
- [ ] #3 The behaviour is documented in docs/WITH_USER_ENV.md
<!-- AC:END -->
