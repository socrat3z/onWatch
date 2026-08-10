---
id: DRAFT-1
title: Collapse provider managers onto one supervisor
status: Draft
assignee: []
created_date: '2026-08-10 19:23'
labels:
  - review-multi-account
  - rev-p4-1
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: medium
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S2 | Lens: Architect | Verdict: DEFER until a fifth provider needs multi-account, or the third manager bug that has to be fixed three times

What & where: Execute the plan Phase 6 - collapse AnthropicAgentManager, AntigravityAgentManager, CodexAgentManager and MiniMaxAgentManager onto one generic supervisor over account.Source. The two new managers are already near-identical (compare internal/agent/anthropic_agent_manager.go:65-135 with internal/agent/antigravity_agent_manager.go:61-111); every fix in P2.1 and P3.2 must currently be written twice.

Why: Review section 4 - duplication is already producing parallel bugs.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P4.1 (deferred - kept as a draft, not open work)
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 One supervisor type drives all account-managed providers
- [ ] #2 Provider-specific logic is confined to a Source plus an agent factory
- [ ] #3 Behaviour is unchanged for Codex profiles and MiniMax accounts
<!-- AC:END -->
