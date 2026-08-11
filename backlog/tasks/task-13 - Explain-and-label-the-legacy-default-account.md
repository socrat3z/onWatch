---
id: TASK-13
title: Explain and label the legacy default account
status: Done
assignee: []
created_date: '2026-08-10 19:23'
updated_date: '2026-08-11 00:00'
updated_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p3-1
dependencies:
  - TASK-1
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 13000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: PO, End-user | Verdict: Do | needs: P1.1

What & where: Give the legacy default account a story. It holds all pre-migration history and is created by the migration backfill (internal/store/provider_account_helpers.go:100-121), while the managers deliberately register discovered aliases as new accounts via CreateOrRestoreProviderAccount (internal/agent/anthropic_agent_manager.go:84). At minimum label it in the picker (Before account split) and document it. Do NOT auto-merge - GetOrCreateProviderAccounts rename-the-lone-default behaviour (internal/store/codex_store.go:900-921) is exactly the shortcut the new managers avoided on purpose.

Why: Review sections 3 and 6 step 6 - upgraders get a permanently frozen account they cannot interpret, sitting in the picker next to their live ones.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.1
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The default account is visually distinguished and explained where it appears
- [x] #2 docs/WITH_USER_ENV.md states what happens to pre-migration history when account discovery is enabled
- [x] #3 No automatic reassignment of historical rows occurs without an explicit user action
<!-- AC:END -->
