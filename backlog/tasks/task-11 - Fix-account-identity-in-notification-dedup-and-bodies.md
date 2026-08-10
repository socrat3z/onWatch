---
id: TASK-11
title: Fix account identity in notification dedup and bodies
status: To Do
assignee: []
created_date: '2026-08-10 19:22'
labels:
  - review-multi-account
  - rev-p2-6
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 11000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: Do

What & where: Drop the AccountID == "1" special case in notificationQuotaKey (internal/notify/notify.go:584-592) in favour of always including provider plus account, and pass the alias rather than the raw ID into the alert body (notify.go:733-735); callers currently pass fmt.Sprintf("%d", accountID) (internal/agent/anthropic_agent.go:717, internal/agent/antigravity_agent.go:203).

Why: Review section 4 finding L8 - a Codex-era "1 means default" assumption is wrong for globally-numbered accounts, "0" from the ambient agent creates a third namespace, and users read Account: 7 instead of an alias.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P2.6
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The same quota on two accounts produces two independent alert states
- [ ] #2 Switching an install from ambient to manager mode does not re-alert quotas that were already alerted
- [ ] #3 Alert bodies name the account alias
<!-- AC:END -->
