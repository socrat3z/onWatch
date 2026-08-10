---
id: TASK-3
title: Bound resident agy processes and measure two-account memory
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-10 21:44'
labels:
  - review-multi-account
  - rev-p1-3
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: high
ordinal: 3000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S1 | Lens: Architect, PO, End-user | Verdict: Do

What & where: Bound resident agy processes, not just concurrent fetches. Either tear down the session after each Fetch when more than one Antigravity account is configured, or share one warm session slot across runners - the current agyPollSerial semaphore (internal/api/antigravity_cli.go:38-40) plus per-runner warmTTL of 5m against a 120s poll interval guarantees permanent residency. Then measure RSS at 1, 2 and 3 accounts and record it in docs/WITH_USER_ENV.md#measured-resource-budget with the date, as CLAUDE.md requires. Correct the two comments that claim the semaphore bounds memory (antigravity_cli.go:38, internal/agent/antigravity_agent_manager.go:17-19).

Why: Review section 2.3 finding L3 - the mitigation in the code does not do what it says; two accounts plausibly exceed the containers 512M limit.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P1.3
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Resident agy process count is provably capped independent of account count
- [x] #2 Measured daemon RSS at 1, 2 and 3 Antigravity accounts is recorded in docs/WITH_USER_ENV.md with a date
- [x] #3 If the measurement exceeds 512M at two accounts, either the limit is raised with a documented reason or Antigravity multi-account is gated off with a startup warning
- [x] #4 No comment claims the fetch semaphore bounds memory
<!-- AC:END -->
