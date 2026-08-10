---
id: TASK-4
title: Fix container login alias validation (path traversal)
status: Done
assignee: []
created_date: '2026-08-10 19:22'
updated_date: '2026-08-10 21:47'
labels:
  - review-multi-account
  - rev-p1-4
dependencies: []
documentation:
  - docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md
priority: high
ordinal: 4000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Severity S1 (security) | Lens: Architect | Verdict: Do

What & where: Replace the shell case glob at docker-entrypoint-with-user-env.sh:21-24 with a check that actually anchors and rejects / and . anywhere - for example a second case rejecting *[!a-z0-9_-]*, or a character-class loop. Extend scripts/test-entrypoint-dispatch.sh:68-71 with the cases that currently pass: wo/../../etc, ab/cd, a.b, a 33-character name, and the empty string. CLAUDE.md requires the test script to change in the same commit as the allowlist.

Why: Review section 2.5 finding L4 - ONWATCH_LOGIN_ACCOUNT=wo/../../etc passes validation and becomes HOME, so a login CLI writes credentials outside the auth volume. The shell case pattern * matches any string including slashes, so only the first two characters are actually checked.

Source: docs/reviews/MULTI_ACCOUNT_MULTI_ANGLE_REVIEW.md section 7, item P1.4
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every rejected-name case (wo/../../etc, ab/cd, a.b, 33 chars, empty) exits 64 before any mkdir, chown or exec
- [x] #2 work, personal_2 and a are still accepted
- [x] #3 The shell validator and account.ValidateName accept and reject the same set of names, and a comment in each points at the other
<!-- AC:END -->
