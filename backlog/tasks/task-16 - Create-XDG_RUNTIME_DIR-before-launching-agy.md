---
id: TASK-16
title: Create XDG_RUNTIME_DIR before launching agy
status: To Do
assignee: []
created_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p3-4
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 16000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: Do

What & where: internal/agent/antigravity_agent.go:262-268 points each account at os.TempDir()/onwatch-runtime/antigravity/<name> but never creates it; the entrypoint creates only the default path (docker-entrypoint-with-user-env.sh:89-91).

Why: Review section 4 finding L7 - GNOME Keyring requires an existing 0700 runtime directory, so per-account keyring state may silently fail for every account except default.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.4
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The daemon creates the per-account runtime directory with mode 0700 before launching agy
- [ ] #2 A second Antigravity account persists its keyring state across a daemon restart
<!-- AC:END -->
