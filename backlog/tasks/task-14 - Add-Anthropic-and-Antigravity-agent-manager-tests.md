---
id: TASK-14
title: Add Anthropic and Antigravity agent manager tests
status: Done
assignee: []
created_date: '2026-08-10 19:23'
updated_date: '2026-08-11 00:00'
labels:
  - review-multi-account
  - rev-p3-2
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
ordinal: 14000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: Do

What & where: internal/agent/ has codex_agent_manager_test.go and minimax_agent_manager_test.go but nothing for the two new managers. Cover the plan test matrix supervisor row (add, remove, restore, failed build, reload race, shutdown) plus a -race case that calls SetAuthRoot/SetAccountPollingCheck concurrently with Reload - those fields are read unguarded at internal/agent/anthropic_agent_manager.go:66 and :104.

Why: Review section 4 - a roughly 3000-line changeset added five test functions, against a repo whose first stated objective is TDD-first. Zero coverage of the supervisors, the HTTP layer or the migration.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P3.2
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 anthropic_agent_manager_test.go and antigravity_agent_manager_test.go exist and use temp dirs and fake clients only - no real credential stores, no installed CLIs
- [x] #2 Adding, removing and restoring an account directory is covered for both managers
- [ ] #3 ./app.sh --test passes with -race
- [x] #4 A test asserts one paused account does not pause another
<!-- AC:END -->
