---
id: TASK-8
title: Surface account health in the API and picker
status: To Do
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p2-3
dependencies:
  - TASK-6
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 8000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Designer, End-user | Verdict: Do | after: P2.1

What & where: Surface account health. Definition.Metadata["credentials"] already carries present/missing/unverified (internal/account/anthropic_source.go:31-36, internal/account/antigravity_source.go:25) but the signal stops at a log line (internal/agent/anthropic_agent_manager.go:90). Persist it to provider_accounts.metadata on each Reload, return it from GET /api/accounts (internal/web/handlers.go:93-101), and render it in the picker.

Why: Review section 5 - selecting a credential-less account currently shows an unexplained empty dashboard.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.3
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 GET /api/accounts returns a health field per account
- [ ] #2 An account with missing credentials is visually distinguished in the picker
- [ ] #3 Selecting an unhealthy account shows an explanation naming the expected credential path, not an empty chart
<!-- AC:END -->
