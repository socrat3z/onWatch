---
id: TASK-6
title: Soft-delete accounts that never started
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-11 00:00'
labels:
  - review-multi-account
  - rev-p2-1
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 6000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect, End-user | Verdict: Do

What & where: Reconcile against all known accounts, not just running ones. In both managers teardown loops (internal/agent/anthropic_agent_manager.go:126-134, internal/agent/antigravity_agent_manager.go:102-110), iterate QueryActiveProviderAccounts(provider) and soft-delete any whose directory is absent, in addition to cancelling running agents.

Why: Review section 2.1 finding L5 - an account registered but never started (missing credentials) can never be removed, because the loop iterates m.running rather than the set of known accounts.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.1
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Creating a directory with no credential file, then deleting it, leaves the account row soft-deleted
- [x] #2 A running account whose directory disappears is still cancelled and soft-deleted (no regression)
- [x] #3 An account soft-deleted while its directory is absent is restored on reappearance
<!-- AC:END -->
