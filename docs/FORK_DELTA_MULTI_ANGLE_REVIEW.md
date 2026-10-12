# Fork Delta (fork-overlay vs upstream/main) - Multi-Angle Review & Roadmap

**Date:** 2026-10-12 · **Scope:** everything `fork-overlay` carries on top of `upstream/main`
(merge base `ea66113`, upstream v2.14.11): 40 fork commits, 203 files, +15,318 / -1,366 ·
**Review type:** static (code and git history read; no build or test run - `app.sh` needs a
POSIX shell that is not on this Windows host) · **Depth:** full · **Supersedes:** none ·
**Companion docs:** [FORK_FEATURES.md](FORK_FEATURES.md),
[FORK_ARCHITECTURE_GUIDELINES.md](FORK_ARCHITECTURE_GUIDELINES.md),
[WITH_USER_ENV.md](WITH_USER_ENV.md), [MULTI_ACCOUNT.md](MULTI_ACCOUNT.md)

**The question this review answers:** for each fork-only change, keep it, upstream it, or
remove it? The trigger is the proposal to return the custom Claude (Anthropic) authentication
to stock upstream behaviour.

**Assumption, stated once:** the real deployment is the `with-user-env` container.
[mise.toml](../mise.toml) builds `Dockerfile.with-user-env` and pushes it to a private
registry. Where a verdict would change for a native (non-container) install, the item says so.

---

## 1. What the fork is today

The fork is upstream onWatch plus five clusters of change. The table groups all 203 files.

| # | Cluster | What it adds | Main files | Touches upstream files? |
|---|---------|--------------|------------|-------------------------|
| A | **Custom Anthropic auth** | Cross-process advisory lock on `<creds>.lock`; serialized read -> exchange -> write rotation that adopts a newer stored pair; removal of upstream's "rotate token on usage-API 429" bypass; 1h OAuth-429 escalation alert; startup readiness and refresh-skip diagnostics; `ONWATCH_CLAUDE_CLI_LOCKED` escape hatch; `flock` wrapper around the in-container `claude` | `internal/api/anthropic_lock*.go`, `anthropic_token.go` (+179), `anthropic_oauth.go`; `internal/agent/anthropic_agent.go` (614 lines changed), `anthropic_cc_detect.go`; `internal/procscan/procscan_overlay.go`; `docker-entrypoint-with-user-env.sh:156` | Yes, heavily (`anthropic_agent.go`) |
| B | **Multi-account for Anthropic + Antigravity** (Codex already has it upstream) | `<ROOT>/<alias>` discovery (`ANTHROPIC_AUTH_ROOT`, `ANTIGRAVITY_AUTH_ROOT`, `CODEX_AUTH_ROOT`), per-account agent managers, `account_id` on 4 tables, account-scoped store queries, account picker/rename UI, account identity in notifications and metrics | `internal/account/`, `internal/agent/*_agent_manager.go`, `account_reconcile.go`, `internal/store/fork_schema_overlay.go`, `provider_account_helpers.go`, `anthropic_store.go`, `antigravity_store.go`, `internal/web/accounts_overlay.go`, `handlers.go`, `notify.go`, `metrics.go` | Yes, heavily (both stores, `handlers.go`, `codex_agent_manager.go` +292) |
| C | **`with-user-env` container** | Image with pinned `codex`/`claude` and bootstrapped `agy`, one-shot `*-login` services, entrypoint allowlist, one volume per credential store | `Dockerfile.with-user-env`, `docker-compose.override-with-user-env.yml`, `docker-entrypoint-with-user-env.sh`, `scripts/test-*.sh`, `docs/WITH_USER_ENV.md`, `mise.toml` | No (additive) |
| D | **Provider fixes** | agy >= 1.2.2 local-RPC CSRF flag; procfs port discovery for slim images; agy CLI launch backoff and resident-session cap; DeepSeek/Moonshot return `null` instead of a fake healthy `0` balance; OpenRouter `/api/v1/key` + account-wide credits; poll-cycle backoff for DeepSeek/Kimi/Moonshot/OpenRouter/OpenCode; `setup.go` EOF handling; SQLite `_txlock=immediate` | `antigravity_csrf_overlay.go`, `antigravity_procports.go`, `antigravity_cli.go` (+286), `deepseek_handlers.go`, `moonshot_handlers.go`, `openrouter_*`, `internal/agent/backoff.go`, `setup.go`, `store.go:197` | Partly |
| E | **Dashboard + repo hygiene** | Homepage renamed "Home"/"Harness Overview" and forced first; freshness banner; reset countdowns; no-data card state; Windows binds `127.0.0.1`; `.gitattributes` LF; `app.sh --integration`; parallel test-isolation package `internal/testenv`; Backlog.md tasks; plan docs | `fork_overlay.js/.css` (additive), `app.js` (476 lines changed), `dashboard_tabs.go`, `server_overlay.go`, `internal/testenv/`, `backlog/`, `docs/plans/` | Partly |

### How cluster A replaces stock behaviour

| Situation | Stock upstream | Fork |
|-----------|----------------|------|
| Usage API returns 429 | Exchanges the refresh token for a new access token (fresh rate-limit window) and retries, unless Claude Code is detected running or auto-refresh is off | Logs and waits for the next scheduled poll ([anthropic_agent.go:1013](../internal/agent/anthropic_agent.go#L1013)) |
| Token near expiry | Proactive refresh unless Claude Code is running | Same guard, but the exchange runs under the file lock and adopts a pair someone else already wrote |
| 401/403 with Claude Code running | Skip refresh | Adopt Claude Code's newer pair; exchange anyway only if `ONWATCH_CLAUDE_CLI_LOCKED=1` ([anthropic_agent.go:379](../internal/agent/anthropic_agent.go#L379)) |
| Server omits `refresh_token` | Writes the new pair as returned | Keeps the old refresh token (RFC 6749 §6) |
| OAuth endpoint 429s for hours | Backoff only, no alert | Backoff plus one alert after 1h ([anthropic_agent.go:522](../internal/agent/anthropic_agent.go#L522)) |
| Where credentials are written | Ambient store: keychain/keyring + `$HOME/.claude/.credentials.json` | Per-account file for named accounts; ambient store, under the lock, otherwise |

### The container-specific wiring (load-bearing for the revert decision)

- The image hard-sets `ANTHROPIC_AUTH_ROOT=/auth/claude`, `ANTIGRAVITY_AUTH_ROOT=/auth/antigravity`,
  `CODEX_AUTH_ROOT=/auth/codex`, `ONWATCH_CLAUDE_CLI_LOCKED=1` and `HOME=/home/nonroot`
  ([Dockerfile.with-user-env:215-228](../Dockerfile.with-user-env#L215-L228)).
- With `ANTHROPIC_AUTH_ROOT` set, the stock Anthropic client is never built
  ([main.go:1068](../cmd/onwatch/main.go#L1068)). Every Claude account, `default` included, is
  polled by the fork's `AnthropicAgentManager` from `/auth/claude/<alias>/.claude/.credentials.json`.
- `claude-login` writes into the `claude-auth` volume under `/auth/claude/<alias>`
  ([entrypoint:90](../docker-entrypoint-with-user-env.sh#L90)). The daemon's own `$HOME` is
  `/home/nonroot`, which is **not** on any volume.

---

## 2. State machines

### 2a. Anthropic credential lifecycle - stock (as implemented upstream)

```
            poll ok                     usage 429
  [VALID] ---------> [VALID]  [VALID] -----------> CC running? --yes--> [VALID] (wait next poll)
     |                                                  |
     | expiring soon & !CC                             no
     v                                                  v
  [REFRESHING] --ok--> write keychain+file --> [VALID]  [REFRESHING] (rotation to dodge limit)
     |   \--invalid_grant--> [PAUSED: re-login]
     \--oauth 429--> [BACKOFF] --timer--> [VALID]
```

### 2b. Anthropic credential lifecycle - fork (as implemented)

```
  [VALID] --usage 429--> [VALID] (no rotation; next poll)
     |
     | expiring soon & !CC & !backoff
     v
  [LOCK WAIT <=30s] --timeout--> [VALID] (skip, logged)
     |
     v
  [READ STORE] --access != expected--> [ADOPT pair] --> [VALID]
     |
     v
  [EXCHANGE] --ok--> [WRITE] --ok--> [VALID]
     |                  \--fail--> [VALID, unsaved] (PersistErr; retry write; supersededToken guard)
     |--invalid_grant--> [RE-READ] --newer pair--> [ADOPT] | else [PAUSED]
     \--oauth 429--> [BACKOFF] --1h unbroken--> [BACKOFF + ALERTED]
```

### 2c. Fork maintenance lifecycle (merging upstream)

```
  upstream release --> git merge upstream/main --> conflicts in touched upstream files?
                                                      |
              additive overlay files only ----------- no ----> smoke --> done
              upstream files edited (A, B, D, E) ---- yes ---> manual resolution --> risk of
                                                               silently dropping an upstream fix
```

Every item in §7 aims to move code from the "yes" branch to the "no" branch: revert it, move it
into an overlay file, or get it merged upstream.

### Latent defects the diagrams and diff expose

1. **S1 (revert hazard) - a naive revert of cluster A silently drops Anthropic in the container.**
   Stock reads `$HOME/.claude/.credentials.json` (upstream `anthropic_token_unix.go`). In the
   container `$HOME` is `/home/nonroot`, while the logins live in `/auth/claude/<alias>/.claude/`.
   Reverting the agent without re-plumbing the image means stock finds no credentials and the
   provider never starts. There is no error, only a missing tab. This is why §7 P1.3 exists.
2. **S2 - multi-account history merges on revert.** The fork backfills `account_id` and scopes
   every Anthropic query by it. Stock queries have no account predicate, so if more than one
   Claude account was ever tracked, stock's "latest snapshot" and cycles would mix rows from
   all of them without labels.
3. **S2 - `AGENTS.md` is a corrupted symlink.** In git it is still mode `120000` (symlink) but
   its target is now `"CLAUDE.md\n\n## Test environment safety ..."`. Windows (with
   `core.symlinks=false`) shows a text file, so the edit looked fine. On Linux and macOS the
   checkout is a dangling symlink, so agents there lose both CLAUDE.md and the new safety
   rules.
4. **S2 (predicted) - the fork's account schema diverges from upstream's convention.** Upstream
   multi-account tables use `account_id INTEGER NOT NULL DEFAULT 1` and treat `1` as the
   default account. The fork adds `account_id ... DEFAULT 0` to the Anthropic and Antigravity
   tables, then backfills each to that provider's autoincrement row in the shared
   `provider_accounts` table ([fork_schema_overlay.go:16](../internal/store/fork_schema_overlay.go#L16)).
   That row is not necessarily `1`. When upstream adds `account_id` to these tables, its
   "duplicate column" guard will skip the ALTER, and its `= 1` assumption may not hold. Not
   verified, because upstream has not done it yet.
5. **S2 - detection fork of `procscan`.** `ClaudeCodeProcess` calls the fork's `procscan.Match`, a
   copy of upstream's `RunningContext` logic
   ([procscan_overlay.go](../internal/procscan/procscan_overlay.go)). Any upstream fix to Claude
   Code detection will no longer reach the fork. Reverting cluster A removes this file.
6. **S3 - `SYNTRACK_HOST=0.0.0.0` is silently rebound on Windows.**
   [main.go:728](../cmd/onwatch/main.go#L728) forces `127.0.0.1` unless `ONWATCH_HOST` is set,
   so the legacy alias cannot request a wildcard bind. The same default is also implemented in
   [config.go:597](../internal/config/config.go#L597) and
   [server_overlay.go:10](../internal/web/server_overlay.go#L10).

---

## 3. Senior Product Owner perspective

**What's genuinely good.** The fork solves a real gap: upstream cannot authenticate Claude,
Codex or Antigravity inside a container. Cluster C makes a headless server deployment possible,
and that is the fork's reason to exist. The balance-provider fixes (DeepSeek and Moonshot
showing "--" instead of a fake healthy `$0.00`, OpenRouter account credits) fix promises the
dashboard was breaking.

**Where the product story breaks.**

- **The cost/benefit of cluster A is upside down for this deployment.** Its whole design
  defends one scenario: onWatch and a natively installed Claude Code sharing one refresh token.
  In the container that scenario does not occur. The daemon's credentials come from a dedicated
  `claude-login` grant, and the only other `claude` that touches them is the one-shot login.
  The fork pays about 1,000 lines plus an image env flag to coordinate with a process that
  almost never runs.
- **Upstream already ships what the fork claims as new.** `provider_accounts`, Codex
  multi-account, OpenRouter, hermetic test homes (`internal/testutil/testhome`) and a Codex
  "auto quota-starter" all exist upstream. [FORK_FEATURES.md](FORK_FEATURES.md) presents
  several of them as fork features.
- **The fork's own docs describe things that are not built.** FORK_FEATURES §4 "Quota Window
  Primer" describes a feature that exists only as a proposal
  ([plan](plans/QUOTA_WINDOW_PRIMER_MULTI_ANGLE_REVIEW.md), status: proposed). §3 promises "Full
  Jitter", but [backoff.go](../internal/agent/backoff.go) is deterministic. §2 says the image
  installs Node.js and mounts host directories, while CLAUDE.md forbids Node and the compose
  file mounts no host paths.

**Jobs-to-be-done.** The one user (the operator) wants three things: see every subscription's
quota on one page, never be logged out of Claude Code by the monitor, and pull upstream
releases without an afternoon of conflict resolution. Cluster A serves the second job for a
deployment shape that is not in use, at a direct cost to the third.

> ⚔ **PO** wants stock Claude auth for less code and easier merges · **Architect** points out
> the container routes every Claude account through the fork's manager, so "stock" is a
> re-plumb, not a `git checkout` → resolved: §7 P1.2 + P1.3

**PO mini-roadmap.** *Now:* revert cluster A (P1.2-P1.3). *Next:* upstream the generic fixes
(Phase 3). *Later:* keep multi-account only where a second account actually exists (P1.1, P4.2, P4.3).

---

## 4. Principal Architect & Software Engineer perspective

**Strengths worth protecting.**
- The overlay discipline is real where it was applied: `server_overlay.go`, `main_overlay.go`,
  `fork_schema_overlay.go` and `fork_overlay.js/.css` are single-hook seams, and the schema
  overlay is idempotent.
- The container trust boundary is careful: login services get no `.env` and no `/data`, the
  entrypoint allowlists exact commands, and the CLIs are pinned by SHA-256.
- The agy CSRF and procfs fixes are well reasoned and version-gated
  ([antigravity_csrf_overlay.go](../internal/api/antigravity_csrf_overlay.go)).
- The cluster A lock code is correct as code. The lock is bounded, honours context
  cancellation, and handles `PersistErr` so a spent refresh token is never dropped. The
  problem is its cost, not its correctness.

**Risks and debt, ranked.**

| Sev | Finding | Evidence |
|-----|---------|----------|
| S1 | Revert hazard: stock auth cannot see container credentials (§2 defect 1) | [Dockerfile.with-user-env:220,227](../Dockerfile.with-user-env#L220), [main.go:1068](../cmd/onwatch/main.go#L1068) |
| S2 | Largest merge-conflict surface: `anthropic_agent.go` (614 lines), `handlers.go` (302), `app.js` (476), `codex_agent_manager.go` (292), `antigravity_cli.go` (286), both stores. Each upstream release that touches these needs a manual merge, and the fork's own guideline C1/C5 targets are missed | `git diff --stat upstream/main...fork-overlay` |
| S2 | Duplicate test infrastructure: `internal/testenv` plus six copies of `setTestUserHome` (in `cmd/onwatch`, `agent`, `api`, `config`, `update`, `web`), and upstream tests rewritten to use them. Upstream's `testhome.SetTestHome` already redirects HOME, USERPROFILE and LOCALAPPDATA and clears the override env vars | `internal/testutil/testhome/testhome.go` on upstream |
| S2 | Schema convention drift (§2 defect 4) | [fork_schema_overlay.go:16](../internal/store/fork_schema_overlay.go#L16) |
| S2 | `procscan` logic forked (§2 defect 5) | [anthropic_cc_detect.go:111](../internal/agent/anthropic_cc_detect.go#L111) |
| S2 | `AGENTS.md` symlink corrupted (§2 defect 3) | `git ls-files -s AGENTS.md` → `120000` |
| S3 | `_txlock=immediate` changes the locking of every SQLite transaction inside an upstream file, and FORK_FEATURES does not mention it. The rationale (avoiding `SQLITE_BUSY_SNAPSHOT`) is plausible and generic | [store.go:197](../internal/store/store.go#L197) |
| S3 | `golang.org/x/sys` was promoted to a direct dependency only for the two lock files | `go.mod`, grep shows no other importer |

**Coupling inside cluster A/B.** Stock auth and Anthropic multi-account cannot be separated
cleanly. Stock `applyRefreshedTokens` always writes to the ambient store. A named account
running on stock auth would therefore write its rotated tokens into the default account's
store, which corrupts both accounts. Keeping Anthropic multi-account requires at least the
`credsWrite` seam ([anthropic_agent.go:630-640](../internal/agent/anthropic_agent.go#L630-L640)).
The two coherent options:

- **Option 1 (recommended, needs one Claude account):** revert cluster A *and* the Anthropic part
  of cluster B. The container runs stock auth against the default login.
- **Option 2 (needs two or more Claude accounts):** stock 429 policy and no lock, but keep the
  per-account manager and its file-scoped writer (`credsWrite`) and file-scoped refresh.
  Roughly the lock, the adoption, the escalation and the flock wrapper are deleted; the manager
  and store scoping stay.

> ⚔ **Engineer** wants to keep the no-rotate-on-429 guard because it is the part that actually
> prevented logouts · **PO** wants stock → resolved: in the container the daemon's grant is
> not shared, so stock rotation logs no one out; the guard is recorded as §7 P4.6
> (*Skip unless* onWatch ever runs natively next to Claude Code)

**Testing gaps.** No test covers the container wiring end to end, from the `claude-login`
volume layout to a successful Anthropic poll. `scripts/test-with-user-env-image.sh` checks
the image's binaries and the dispatch allowlist, not polling. That gap is why defect 1 would
not be caught.

**Architect mini-roadmap.** *Now:* P1.2, P1.3, P2.1, P2.2. *Next:* Phase 3 upstream PRs (each
one deletes fork diff). *Later:* P4.4 (move `app.js` edits into the overlay), P4.5 (schema
convention).

---

## 5. Senior Designer / UX perspective

**What works.** Showing "--" with a neutral `unknown` badge instead of a healthy-looking zero
([style.css](../internal/web/static/style.css) `data-status="unknown"`, DeepSeek/Moonshot
handlers) is the right call for a monitoring product. The freshness banner and reset
countdowns answer "is this number current?". The account-note element
([dashboard.html](../internal/web/templates/dashboard.html) `#provider-account-note`) explains
an account with no data instead of showing an empty page.

**UX gaps.**
- **The "Home" rename is opinionated, and it lives in upstream files.** "All" becomes "Home",
  the page title becomes "Harness Overview", and the tab is forced first even against a saved
  order ([dashboard_tabs.go](../internal/web/dashboard_tabs.go)). This is a reasonable personal
  preference, but it is implemented in upstream Go and templates rather than the overlay.
- **An account picker with one account is noise.** Task-9 deliberately shows the picker even
  with one account. After Option 1 there is one Claude account, so the Anthropic picker
  disappears with the revert (`accounts_overlay.go:57` limits the API to
  `anthropic|antigravity`).
- **After a revert, the dashboard should not go quietly blank.** If P1.3 is missed, the Anthropic
  tab simply vanishes, which is the worst failure for a monitor. P1.3's done-when includes a
  visible first poll.

> ⚔ **Designer** wants to keep the homepage polish · **Architect** wants `app.js` back to
> upstream → resolved: keep the behaviour and move it into `fork_overlay.js` (§7 P4.4); the
> features stay and the conflict surface goes

**Designer mini-roadmap.** *Now:* nothing blocking. *Next:* P3.1 (null balances upstream, so
everyone gets it). *Later:* P4.4.

---

## 6. End-user perspective - journey walkthrough

**Persona:** Sam runs onWatch as a container on a small home server, tracks Claude, Codex,
Antigravity and a few API-key providers, and pulls upstream about weekly.

| Step | Today (fork) | After the roadmap |
|------|--------------|-------------------|
| 1. `docker compose --profile login run --rm claude-login` | ✅ works; wrapped in `flock` | ✅ works; no `flock` |
| 2. Daemon starts and finds the login | ✅ via `ANTHROPIC_AUTH_ROOT` manager | ✅ via `$HOME/.claude` → default login (P1.3) |
| 3. Usage API returns 429 | ⚠️ waits for the next poll, so data is sparser; there is no Claude Code statusline in the container to fall back on | ✅ stock rotates for a fresh window; nobody else holds this grant |
| 4. Token expires overnight | ✅ proactive refresh under the lock | ✅ stock proactive refresh |
| 5. OAuth endpoint blocks the server IP for hours | ✅ alert after 1h | ⚠️ backoff only, no alert (accepted loss; see P4.6) |
| 6. Weekly `git merge upstream/main` | ❌ conflicts in `anthropic_agent.go`, `handlers.go`, `app.js`, stores, upstream tests | ⚠️ fewer and smaller conflicts; disappear as Phase 3 PRs land |
| 7. Read FORK_FEATURES to remember what the fork does | ❌ describes things that do not exist | ✅ accurate (P2.4) |
| 8. Open AGENTS.md from a Linux checkout | ❌ dangling symlink | ✅ (P2.1) |

**Verdict.** The product delivers on the daily loop (steps 1-4). It fails on maintenance (steps 6-8), and that is where Sam
feels the fork. Reverting cluster A trades one alert (step 5) for a large drop in merge pain
and slightly denser Claude data (step 3).

---

## 7. Consolidated cross-perspective roadmap

**Legend:** `- [ ] **P<phase>.<n>**` stable, permanent ID (never renumbered) · `[x] <sha>` =
done · `[x] ~~item~~ <reason>` = consciously rejected · **S1** correctness/data-loss/security,
**S2** structural debt/drift, **S3** friction/polish · lenses: PO, Arch, Eng, UX, User ·
**Verdict:** Do / Quick win / Defer: \<trigger\> / Skip unless \<condition\> ·
**needs:** hard prerequisite · **after:** recommended order.

**Open decisions**
- **D1 - how many accounts per provider are really in use?** Option 1 or 2 (§4) and items
  P1.4, P4.2, P4.3 depend on the answer. Check with
  `docker compose exec onwatch ls /auth/claude /auth/codex /auth/antigravity`. **Default
  assumed: one Claude account → Option 1.**


### Progress log (2026-10-12)

Implemented in the working tree on `fork-overlay`; **nothing is committed yet**, so items carry "working tree" instead of a SHA. Replace it with the commit short-SHA when you commit.

**Verification run:** `go vet ./...` clean; `go test` passes for every package, and `go test -race ./...` passes for every package (two packages showed a transient "build failed" in the first combined run and passed on re-run). `app.sh` could not be used (WSL has no distro on this host), so `go test` was run directly. Not verified: the Docker image build and a live Claude poll in the container, and the entrypoint symlink on a real Linux filesystem (busybox on Windows cannot create it).

**What changed against the plan**
- P1.1 not done: D1 is unanswered. Option 1 (one Claude account) was assumed. If more than one Claude account exists under `/auth/claude`, only `default` is polled now; the others' history stays in the DB but is no longer shown for Anthropic.
- P1.2 also removed the Anthropic account manager's tests and converted the multi-account tests that used Anthropic as their example to Antigravity.
- P2.2 left four small fork diffs in test files on purpose: the Windows loopback listeners (`web/server_test.go`, `cmd/onwatch/root_coverage3_test.go`), `web/timezone_static_test.go` (reads `fork_overlay.js` too), `internal/service/launchd_test.go` (Windows skip), and the files that test kept features.
- P2.3 kept the Windows default in `config.go` only; `server.go` is stock apart from the one route-registration hook.
- P3.1-P3.5 (upstream PRs) are not started: opening PRs is outward-facing, so they wait for your go-ahead.
- P4.2-P4.5 stay deferred on their stated triggers.
### Phase 1 - Stock Claude auth with nothing lost

- [ ] **P1.1** Inventory accounts per provider (answers D1) · **S2** · PO, Arch  *(OPEN - needs you: cannot inspect the running container from here; Option 1 was assumed, see Progress log)*
  - *What & where:* list `/auth/claude/*`, `/auth/codex/*`, `/auth/antigravity/*` in the running
    container. Run `SELECT provider, name, deleted_at FROM provider_accounts` and
    `SELECT account_id, COUNT(*) FROM anthropic_snapshots GROUP BY account_id` on
    `/data/onwatch.db`.
  - *Why:* §4 coupling; §2 defect 2.
  - *Done when:*
    - the number of live accounts per provider is recorded in this doc next to D1
    - the Option 1 / Option 2 choice is ticked
  - *Verdict:* **Quick win**

- [x] **P1.2** (working tree, uncommitted) Revert cluster A and the Anthropic half of cluster B to upstream · **S2** · PO, Arch, Eng
  - *What & where:* `git checkout upstream/main --` on `internal/api/anthropic_oauth.go`,
    `anthropic_token.go`, `anthropic_token_windows.go`, `anthropic_types.go`,
    `internal/agent/anthropic_agent.go`, `anthropic_cc_detect.go`,
    `anthropic_authrecovery_test.go`, `anthropic_ratelimit_test.go`,
    `anthropic_statusline_test.go`, `internal/store/anthropic_store.go`,
    `internal/tracker/anthropic_tracker.go`.
    Delete `internal/api/anthropic_lock*.go`, `anthropic_rotation_test.go`,
    `anthropic_oauth_ratelimit_body_test.go`, `internal/agent/anthropic_agent_manager*.go`,
    `anthropic_credswriter_test.go`, `anthropic_ratelimit_escalation_test.go`,
    `anthropic_refresh_diag_test.go`, `internal/account/anthropic_source.go`,
    `internal/procscan/procscan_overlay.go`.
    Then remove the Anthropic hooks:
    - the `AnthropicAuthRoot` field and its uses in `config.go`
    - [main.go:1068](../cmd/onwatch/main.go#L1068) and [main.go:1298-1303](../cmd/onwatch/main.go#L1298-L1303)
    - the `anthropic` branch of `main_overlay.go`
    - the Anthropic account-ID hunks in `handlers.go`, `menubar.go` and `migration.go`
    - `anthropic` in `accounts_overlay.go:57`
    - the Anthropic tables in `fork_schema_overlay.go` (leave existing columns in place;
      stock ignores them)

    Run `go mod tidy` so `golang.org/x/sys` returns to indirect. Fix any fallout in shared fork
    tests (`store/multi_account_test.go`, `agent/coverage_final_test.go`,
    `web/*accounts*_test.go`); these appear to reference Anthropic and were not individually
    verified.
  - *Why:* §3 cost/benefit; §4 risk table rows 2 and 5.
  - *Done when:*
    - `git diff upstream/main -- internal/api/anthropic_* internal/agent/anthropic_* internal/store/anthropic_store.go internal/tracker/anthropic_tracker.go` is empty
    - `rg "AnthropicAuthRoot|ONWATCH_CLAUDE_CLI_LOCKED|RefreshAnthropicCredentials|procscan.Match"` finds nothing in Go code
    - `./app.sh --test` passes with `-race`
  - *Verdict:* **Do** (Option 1); under Option 2, keep `anthropic_agent_manager.go`,
    `anthropic_source.go`, the store scoping and the `credsWrite` seam, and delete only the
    lock, adoption, escalation and diagnostics.
  - *needs:* P1.1

- [x] **P1.3** (working tree, uncommitted; shell syntax and dispatch test checked, image not built) Re-plumb the container for stock Anthropic auth · **S1** · Arch, UX, User
  - *What & where:*
    - [Dockerfile.with-user-env:220-221](../Dockerfile.with-user-env#L220-L221): drop
      `ANTHROPIC_AUTH_ROOT` and `ONWATCH_CLAUDE_CLI_LOCKED`.
    - [docker-entrypoint-with-user-env.sh](../docker-entrypoint-with-user-env.sh): in the daemon
      branch, before `exec /app/onwatch`, run
      `ln -sfn /auth/claude/default/.claude "$HOME/.claude"`.
    - Replace the `flock` block (lines 148-161) with a plain `exec /usr/local/bin/claude "$@"`.
    - Update `scripts/test-entrypoint-dispatch.sh` if its expectations change.
    - Update `docs/WITH_USER_ENV.md` and the "with-user-env image" and "Anthropic OAuth
      credential rotation" sections of CLAUDE.md.
  - *Why:* §2 defect 1. Without this, P1.2 makes Anthropic silently disappear.
  - *Done when:*
    - after `claude-login`, the daemon log shows an `Anthropic poll complete` line within one interval
    - a forced token expiry triggers a stock proactive refresh that rewrites `/auth/claude/default/.claude/.credentials.json`
    - `docker compose config` still shows `claude-login` with only the `claude-auth` volume
  - *Verdict:* **Do**
  - *needs:* P1.2

- [ ] **P1.4** *(SKIPPED pending P1.1 - only needed if more than one Claude account was ever tracked)* Retire non-default Anthropic history · **S2** · Arch, User
  - *What & where:* if P1.1 found more than one Claude account, back up the DB, then
    `DELETE FROM anthropic_snapshots WHERE account_id NOT IN (0, <default id>)` (cascades to
    quota values) and do the same for `anthropic_reset_cycles`. Soft-delete those
    `provider_accounts` rows.
  - *Why:* §2 defect 2. Stock queries would otherwise mix accounts.
  - *Done when:*
    - `SELECT DISTINCT account_id FROM anthropic_snapshots` returns only the default (and 0)
    - the Anthropic dashboard cycles match the single remaining account
  - *Verdict:* **Skip unless** P1.1 found two or more Claude accounts and Option 1 was chosen
  - *needs:* P1.1, P1.2

- [x] **P1.5** (working tree, uncommitted) Restore the stock Anthropic docs · **S3** · PO
  - *What & where:* restore upstream's "Anthropic Rate Limit Bypass" section in CLAUDE.md and
    the README feature line and 429 troubleshooting paragraph. Remove FORK_FEATURES §5.
  - *Why:* CLAUDE.md is loaded into every agent session; a stale description of the lock would
    misdirect future work.
  - *Done when:*
    - `git diff upstream/main -- CLAUDE.md README.md` contains no Anthropic lines
  - *Verdict:* **Do**
  - *after:* P1.3

### Phase 2 - Stop paying for duplicates and broken metadata

- [x] **P2.1** (working tree, uncommitted) Repair `AGENTS.md` · **S2** · Eng
  - *What & where:* restore the symlink (`git checkout upstream/main -- AGENTS.md`) and move the
    "Test environment safety" rules into CLAUDE.md, or into a new non-symlink file that
    CLAUDE.md references.
  - *Why:* §2 defect 3.
  - *Done when:*
    - `git cat-file -p HEAD:AGENTS.md` prints exactly `CLAUDE.md`
    - the safety rules are readable from a Linux checkout
  - *Verdict:* **Quick win**

- [x] **P2.2** (working tree, uncommitted) Replace `internal/testenv` with upstream `testhome` · **S2** · Arch, Eng
  - *What & where:* delete `internal/testenv/` and the six `setTestUserHome` helpers. Restore
    upstream versions of the rewritten upstream tests (`root_coverage3_test.go`,
    `config_test.go`, `update_*_test.go`, ...). Port any isolation that `testhome` truly
    lacks (for example XDG data paths, if they are needed) as a PR to upstream `testhome`
    rather than as a fork copy.
  - *Why:* §4 risk table row 3.
  - *Done when:*
    - `rg "testenv\.|setTestUserHome"` finds nothing
    - every test file the fork modified but did not create is identical to upstream, or
      differs only for a still-kept feature
    - `./app.sh --test` passes on Windows and Linux
  - *Verdict:* **Do**
  - *after:* P1.2

- [x] **P2.3** (working tree, uncommitted) Single source for the Windows bind default · **S3** · Eng
  - *What & where:* keep the default in [config.go:597](../internal/config/config.go#L597) only.
    Delete the override at [main.go:728-730](../cmd/onwatch/main.go#L728-L730) and
    `forkDefaultServerHost` in `server_overlay.go`. Restore upstream `server.go`.
  - *Why:* §2 defect 6.
  - *Done when:*
    - `SYNTRACK_HOST=0.0.0.0` on Windows binds `0.0.0.0`
    - with no host variable set, Windows binds `127.0.0.1`
    - `server.go` matches upstream
  - *Verdict:* **Quick win**

- [x] **P2.4** (working tree, uncommitted) Make the fork's docs and backlog true · **S3** · PO
  - *What & where:* rewrite FORK_FEATURES.md from this review's §1 table. Remove the Node.js,
    host-mount, "OpenRouter added", "`provider_accounts` added", "Full Jitter" and "Quota Window
    Primer" claims. Mark backlog task-21 Done (implemented in `dd81572`). Move `docs/plans/*`
    to an archive or delete them. The primer plan overlaps upstream's Codex auto
    quota-starter.
  - *Why:* §3 "docs describe things that are not built".
  - *Done when:*
    - each FORK_FEATURES section names a file that exists and behaviour that the file implements
    - `backlog task list` shows task-21 as Done
  - *Verdict:* **Quick win**
  - *after:* P1.2

### Phase 3 - Upstream the generic fixes (each merged PR deletes fork diff)

- [ ] **P3.1** PR: balance providers report "no data" instead of zero · **S2** · PO, UX
  - *What & where:* `deepseek_handlers.go`, `moonshot_handlers.go`, the `snapshotAt` additions
    across handlers, and the `.status-badge[data-status="unknown"]` rule.
  - *Why:* §5 "what works". This fixes a misleading healthy zero for every upstream user.
  - *Done when:*
    - an upstream PR is merged
    - after the next merge, these files match upstream
  - *Verdict:* **Do**

- [ ] **P3.2** PR: OpenRouter `/api/v1/key` + account credits · **S2** · PO
  - *What & where:* `openrouter_client.go`, `openrouter_types.go`, `openrouter_store.go`. Move
    the three `ALTER TABLE openrouter_snapshots` statements into upstream's `migrateSchema`
    in the PR, then drop them from `fork_schema_overlay.go`.
  - *Why:* §1 cluster D.
  - *Done when:*
    - the PR is merged
    - the fork's OpenRouter files and the OpenRouter part of the overlay are gone
  - *Verdict:* **Do**

- [ ] **P3.3** PR: agy CSRF flag + procfs port discovery · **S2** · Arch
  - *What & where:* `antigravity_csrf_overlay.go`, `antigravity_procports.go`,
    `antigravity_client.go:541`, and the matching part of `antigravity_cli.go`.
  - *Why:* without the CSRF flag, agy >= 1.2.2 rejects every RPC with 401. Upstream users on
    `ANTIGRAVITY_SOURCE=cli` would hit the same failure.
  - *Done when:*
    - the PR is merged
    - the fork's diff in `antigravity_cli.go` shrinks to the multi-account env injection only
  - *Verdict:* **Do**

- [ ] **P3.4** PR bundle: small correctness fixes · **S3** · Eng
  - *What & where:* `setup.go` prompt EOF handling (the old loop never exits on closed stdin),
    `app.sh --integration`, `_txlock=immediate` ([store.go:197](../internal/store/store.go#L197)),
    `internal/agent/backoff.go` and its five call sites, `codex_profiles.go` not-a-directory
    check, `.gitattributes`.
  - *Why:* §4 risk table rows 7 and 8.
  - *Done when:*
    - each fix is either merged upstream or listed in FORK_FEATURES with a reason it stays
      fork-only
  - *Verdict:* **Quick win**

- [ ] **P3.5** PR: notification dedup keyed on the real default account · **S2** · Arch
  - *What & where:* the `notificationQuotaKey` / `defaultAccountID` / `accountLabel` part of
    `notify.go`. Upstream hard-codes `"1"` as the default account, but `provider_accounts` IDs
    are a shared autoincrement. That appears to affect upstream's own multi-account providers
    (MiniMax, Codex), which was not verified against upstream data.
  - *Why:* §2 defect 4, the same root cause.
  - *Done when:*
    - an upstream issue or PR exists
    - `notify.go` matches upstream after it merges
  - *Verdict:* **Do**

### Phase 4 - Keep, but contain

- [x] **P4.1** (decision recorded) Keep the `with-user-env` image · **S3** · PO, User
  - *What & where:* no change beyond P1.3. It is additive and conflict-free, and it is the
    reason the fork exists.
  - *Why:* §3 "what's genuinely good".
  - *Done when:*
    - this review's §1 cluster C still describes the image after P1.3
  - *Verdict:* **Do** (keep)

- [ ] **P4.2** Antigravity multi-account · **S2** · Arch
  - *What & where:* `ANTIGRAVITY_AUTH_ROOT`, `antigravity_agent_manager.go`, the account
    scoping in `antigravity_store.go`. The container's per-account `HOME` and
    `XDG_RUNTIME_DIR` layout depends on it even for a single account.
  - *Why:* §4 risk table row 2. `antigravity_store.go` is a large upstream-file edit.
  - *Done when:*
    - the decision is recorded: keep (two or more Google accounts), or collapse to stock
      `HOME`-based agy with a P1.3-style symlink
  - *Verdict:* **Defer: first upstream merge that conflicts in `antigravity_store.go`** (keep until then)
  - *needs:* P1.1

- [ ] **P4.3** Codex `CODEX_AUTH_ROOT` native discovery · **S3** · Arch
  - *What & where:* `codex_agent_manager.go` (+292 in an upstream file),
    `codex_credentials.go`. Stock already reads `CODEX_HOME=/auth/codex/default`, so
    discovery adds value only for a second account.
  - *Why:* §4 risk table row 2.
  - *Done when:*
    - `codex_agent_manager.go` and `codex_credentials.go` match upstream
    - with `CODEX_AUTH_ROOT` removed from the image, the default Codex account still polls
  - *Verdict:* **Skip unless** P1.1 finds only one Codex account; in that case remove it.
  - *needs:* P1.1

- [ ] **P4.4** Move the fork's `app.js` / `dashboard_tabs.go` / template edits into the overlay · **S2** · Arch, UX
  - *What & where:* 476 changed lines in `app.js`, the "Home" ordering in `dashboard_tabs.go`,
    and the "Harness Overview" title in `dashboard.html`. Re-express them as `fork_overlay.js`
    hooks and DOM changes, per guideline C5.
  - *Why:* §5 ⚔ callout.
  - *Done when:*
    - `git diff upstream/main -- internal/web/static/app.js` contains only hook calls (target at most 20 lines)
    - the homepage looks and behaves the same
  - *Verdict:* **Defer: next upstream merge with an `app.js` conflict**
  - *after:* P1.2

- [ ] **P4.5** Align the account schema with upstream's `DEFAULT 1` convention · **S2** · Arch
  - *What & where:* `fork_schema_overlay.go` for the remaining Antigravity tables.
  - *Why:* §2 defect 4.
  - *Done when:*
    - the fork migration produces the same column definition upstream uses for its other
      multi-account tables
    - a test proves that an upstream-style `ALTER ... DEFAULT 1` on top of it is a no-op
  - *Verdict:* **Defer: upstream adds `account_id` to any Anthropic or Antigravity table**
  - *after:* P4.2

- [x] ~~**P4.6** No-rotate-on-429 guard for native installs~~ container-only (assumed; reopen if onWatch ever runs natively beside Claude Code) · **S2** · Eng, User
  - *What & where:* the cluster A line "usage 429 → wait, don't rotate" (fork
    [anthropic_agent.go:1005-1014](../internal/agent/anthropic_agent.go#L1005-L1014)). Native
    risk: upstream's Windows detection matches the image name `claude.exe` only. An
    npm-installed Claude Code appears to run as `node.exe`, so stock would rotate underneath
    it. This is inferred and not tested.
  - *Why:* §4 ⚔ callout.
  - *Done when:*
    - either the fork never runs natively, and this item is ticked as rejected
    - or a minimal guard is offered upstream as a setting, not as a fork patch
  - *Verdict:* **Skip unless** onWatch is run natively on a machine that also runs Claude Code

---

## 8. Summary scorecard

Grade bands: **A** solid core, no open S1, S2s known and planned · **B** solid core with known
unfixed S2s · **C** core loop broken somewhere or an open S1 · **D/F** does not do its job.

| Dimension | Grade | Rationale |
|-----------|-------|-----------|
| Domain model | B | Account model is coherent, but it diverges from upstream's `DEFAULT 1` convention (defect 4) |
| Engineering | B | Fork code is careful and well tested; the cost is volume and duplication, not bugs |
| Integration (upstream mergeability) | C | About 2,000 lines edited inside upstream files, upstream tests rewritten, `procscan` and test helpers forked; each upstream release is a manual merge |
| Product completeness | B | The container deployment works end to end; the docs promise unbuilt features |
| UX/UI | B | The no-data states and freshness indicators are real improvements; the "Home" changes live in upstream files |
| Maintainability | C | FORK_FEATURES is inaccurate, AGENTS.md is broken on Unix, the backlog is stale, and cluster A doubles the auth surface for a scenario the deployment does not have |

**Bottom line:** yes, go back to stock Claude auth. In this deployment the daemon owns a
dedicated login grant, so the lock-and-adopt machinery guards a conflict that does not happen,
and it costs the largest single merge surface in the fork. Do it as **P1.2 + P1.3 together**,
never P1.2 alone: the image routes every Claude account through the fork's manager, so a bare
revert would make Anthropic quietly disappear. Answer D1 (P1.1) first. It takes a minute and
decides whether this is a clean revert (Option 1) or a trimmed one (Option 2).
