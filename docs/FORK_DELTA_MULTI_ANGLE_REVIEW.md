# Fork Delta and Upstream Feature Parity - Multi-Angle Review & Roadmap

**Date:** 2026-10-29 (Europe/Bucharest)
**Scope:** fork-overlay at 28e92c6 versus freshly fetched upstream/main at 7e2343e (v2.14.16).
**Review type:** static: source, tests, documentation and Git history inspected; no application, image, test suite, live credentials or production database executed/accessed. Behavioral findings below are source-derived, with integration uncertainties explicitly identified.
**Depth:** full. **Supersedes:** the 2026-10-12 review at this path.
**History:** permanent roadmap IDs and all nine resolved/rejected entries are retained in section 7. Historical validation claims are not new validation. No tracker contents were inspected or changed.
**Companions:** [fork features](FORK_FEATURES.md), [architecture rules](FORK_ARCHITECTURE_GUIDELINES.md), [container setup](WITH_USER_ENV.md), [accounts](MULTI_ACCOUNT.md).

**Decision:** move toward upstream feature by feature. Anthropic auth and agy CSRF handling have already converged. Upstream profiles can replace the custom Codex discovery mechanism, including multiple accounts, after credential conversion. Multiple independent Antigravity accounts, container-local login services, account-wide OpenRouter credits and the particular compact Home dashboard remain real differences. A wholesale upstream binary swap against the current database is not a safe migration plan.

**Most consequential finding:** an upstream Antigravity poll can update/close active cycles belonging to multiple fork accounts because stock SQL has no account predicate. Independently, the fork's two competing Codex discovery paths can switch which real account owns an alias after a file change. Address account ownership and rehearse data migration before removing more code.

## 1. What the fork is today

### Measured delta

Upstream is an ancestor of this branch: 45 fork-side commits, zero missing upstream commits. The current diff is **146 files, +11,974 / -529**, including **70 modified upstream files and 76 added files**. This is smaller than the old review's 203-file comparison.

The runtime-source subset (Go excluding _test.go, JavaScript, CSS and HTML) is **56 files, +3,862 / -492**; the larger total includes tests, plans, review documents and tracker artifacts. These are Git line counts, not complexity scores.

The following are byte-identical to upstream in the reviewed comparison:

- Anthropic API/agent files, anthropic_store.go and anthropic_tracker.go.
- The Codex polling agent and Responses quota-starter transport.
- internal/testutil/testhome, go.mod, go.sum and the AGENTS.md symlink payload.

There is still fork Anthropic DDL in [fork_schema_overlay.go:15](../internal/store/fork_schema_overlay.go#L15). Thus “stock Anthropic runtime” does not mean the fork database has a stock schema or that historical named-account rows were reconciled.

### Domain and deployment model

| Entity or boundary | Current responsibility |
|---|---|
| Upstream provider agents/clients/trackers | Poll providers, retain snapshots/cycles, refresh supported OAuth credentials, publish poll outcomes. Most code remains inherited. |
| provider_accounts | Inherited account identities; stock Codex/MiniMax and fork Antigravity share this table. A display alias is distinct from the credential directory and provider identity. |
| Saved Codex profile | Stock JSON credential record in codex-profiles; supports multiple accounts and external-identity deduplication. |
| Native Codex home | Fork-only CODEX_AUTH_ROOT/alias/auth.json discovery plus write-back to that file. It currently competes with saved-profile discovery for the same alias. |
| Antigravity account manager | Fork-only discovery of HOME directories, CLI-only account agents, account-scoped snapshots/cycles/sessions. |
| Fork schema runner | Runs after upstream migrations; adds account columns/indexes and OpenRouter credit columns. It is additive but not semantically reversible by swapping binaries. |
| with-user-env image | Packaging with pinned native login CLIs, dedicated credential volumes, D-Bus/keyring support, login command allowlist and isolated one-shot services. |
| Dashboard overlay | Account picker/alias editing, compact Home layout, provider freshness banner, reset chips and extra balance presentation. |

Primary deployment assumption: the fork container matters, based on the checked-in Docker/Compose workflow; actual deployed account counts and providers are unknown. No deployment was inspected to fill that gap.

### Feature-by-feature replacement matrix

“Equivalent” means the same useful workflow exists. “Partial” names the differences rather than treating a similar label as complete parity.

| Capability | What upstream actually provides | Decision and loss when switching |
|---|---|---|
| Anthropic OAuth refresh, rate-limit behavior and credential persistence | Same runtime implementation. Reads the default Claude credential store; [stock file lookup](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/api/anthropic_token_unix.go#L21). | **Already switched.** Keep default-volume visibility from [entrypoint:155](../docker-entrypoint-with-user-env.sh#L155). No custom auth code remains to remove. |
| agy local-RPC CSRF token | Stock launch arguments and request token propagation; [agyLaunchArgs](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/api/antigravity_cli.go#L226). | **Already switched** by fork commit 28e92c6. Do not recreate the deleted version-detection/CSRF overlay. |
| Antigravity IDE, CLI and “both” sources | Managed warm CLI session, relaunch/idle teardown and IDE fallback; [stock source dispatch](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/agent/antigravity_agent.go#L231). | **Equivalent for one account's basic polling.** Upstream does not replace the fork's multi-account homes, environment allowlist, procfs discovery or global session cap. |
| Codex multiple accounts and account selection | Saved profiles, individual/All accounts UI and account-scoped history; [stock profile manager](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/agent/codex_agent_manager.go#L192), [selector](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/web/static/app.js#L310). | **Use upstream implementation.** Multiple Codex accounts alone are not a reason to retain native-home discovery. Convert files first; see migration below. |
| Automatic native Codex-home discovery | One ambient CODEX_HOME plus saved JSON profiles; [native credentials lookup](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/api/codex_credentials.go#L245). No CODEX_AUTH_ROOT scanner. | **Partial alternative.** Import each native login with stock profile save --auth-file. Lose automatic directory discovery and refresh write-back to every original native home. |
| MiniMax multi-account setup, selection and restore | Already has Add/Edit/Delete/Restore, individual/All selection and account-scoped views; [stock settings UI](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/web/static/app.js#L12082). | **Use upstream.** Remaining fork differences are shared presentation/notification/metrics handling, not the existence of MiniMax multi-account support. |
| Multiple Antigravity accounts, account-scoped history and alias picker | No stock multi-account manager/schema for this provider. | **Keep if two or more accounts matter.** A single upstream instance/source selector is not a replacement. Separate instances with separate databases are an operational alternative, but lose the unified account view. |
| Login CLIs and isolated login services inside the image | Neither stock image bundles codex/claude/agy. Stock offers both an Alpine shell target and a distroless target; [Dockerfile](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/Dockerfile#L52). | **Keep packaging if container-local login matters.** A shell alone is not a login CLI. This can outlive most Go fork changes. |
| OAuth credential use in stock Docker | Stock file readers can use credentials acquired elsewhere, with suitable writable mounts/path settings. | **Possible deployment replacement.** “API keys only” in FORK_FEATURES is too broad. Acquisition inside the stock image is absent; tracking mounted credentials is a separate question. |
| Empty DeepSeek/Moonshot/OpenRouter values | Stock provider-page renderer already has a “No balance data yet” guard; [app.js:2500](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/web/static/app.js#L2500). API defaults still contain synthetic zero values/current time. | **Partial.** Fork null/availability API semantics and Home balance rows are real improvements. Do not claim every stock browser page shows a healthy zero. |
| OpenRouter provider and per-key usage | Already stock, using /api/v1/auth/key; [client:82](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/api/openrouter_client.go#L82). | **Keep only the actual delta:** /key endpoint, optional /credits query, separate stored account totals and balance UI. Upstream cannot currently replace account-wide credits. |
| Account labels in alerts and metrics | Stock already has account-aware Codex/MiniMax telemetry; fork adds display aliases, active-account filtering and Antigravity account series. | **Partial.** Stock Antigravity metrics use account_id=default; fork uses real numeric account IDs. Update Prometheus selectors and accept series/dedup differences if switching; [fork series](../internal/metrics/metrics.go#L555), [stock series](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/metrics/metrics.go#L553). |
| Rate-limit handling for five extra providers | Stock recognizes typed 429 errors; OpenCode skips the failed cycle. Anthropic has a distinct OAuth/time-based policy. | **Keep generic backoff if used**, with poll-outcome repair below. These stock mechanisms do not provide the fork's growing skipped-cycle backoff for all five providers. |
| Data ages, stale cards and stale history | Already has Anthropic/Codex quota ages, stale-card treatment and history error/cache feedback; [freshness](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/web/static/app.js#L875), [All rendering](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/web/static/app.js#L7923). | **Partial.** Fork adds a broader Home banner and per-account summary. Its current implementation has false/missed health states; upstream is a valid fallback if the banner is optional. |
| Reset timing/countdowns | Already present on detail cards and All-view KPI cards; [All KPI reset display](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/web/static/app.js#L7327). | **Presentation choice.** Fork adds compact Home reset chips/minute ticks; it did not introduce reset timing to onWatch. |
| Compact Home-first overview | Stock All has overview charts, insights and collapse controls. | **Choose explicitly.** Fork replaces these with compact metric rows and navigation. Reverting is a UX trade, not pure loss or an equivalent layout. |
| Codex quota-window auto-start | Exact inherited implementation, default-off 5h/weekly toggles and per-account behavior; [stock docs](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/docs/CODEX_SETUP.md#L314). | **Already upstream.** No fork feature to maintain for this job. |
| Primer at a chosen local clock time, including Claude | Neither implementation has the proposed scheduler. [Fork plan](plans/QUOTA_WINDOW_PRIMER_MULTI_ANGLE_REVIEW.md) is marked proposed. | **No implemented feature to migrate.** Reactive Codex auto-start is not an exact substitute for “send at 06:00 on weekdays.” |
| Windows loopback default | Stock accepts ONWATCH_HOST; [config:654](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/config/config.go#L654). | **Can replace with deployment configuration** ONWATCH_HOST=127.0.0.1. Retain source default only if unconfigured native Windows installs need it. |
| Setup EOF exit, profile-directory validation, SQLite immediate transactions, integration flag | Small independent fork changes; [setup:1197](../cmd/onwatch/setup.go#L1197), [profile path:819](../cmd/onwatch/codex_profiles.go#L819), [SQLite:198](../internal/store/store.go#L198), [app.sh:242](../app.sh#L242). | **No complete stock equivalent in this baseline.** Keep or upstream separately; they do not justify the larger account/UI fork. |

OpenRouter's current reference documents /key as the current authenticated key and /credits as account totals requiring a management key. This supports the fork's separation of key limits from purchased credits; it does not mean an ordinary inference key can expose both. The code uses the same configured key for both requests, so arbitrary selected inference-key usage plus a separate management-key balance is not a delivered two-key workflow; [the client shares c.apiKey](../internal/api/openrouter_client.go#L169). [Current-key reference](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key), [credits reference](https://openrouter.ai/docs/api/api-reference/credits/get-remaining-credits).

### Practical migration choices

**Recommended near-term shape:** keep the container/login packaging, move Codex to stock profiles, keep Antigravity account isolation only if actually needed, and treat generic correctness patches separately from cosmetic preferences. A packaging-only derivative image can eventually carry an unmodified upstream onWatch binary; it need not imply a permanent Go fork.

For Codex:

1. Stop the daemon before migration. Retain a consistent database backup and credential/profile-volume backup. Do not run two refresh writers against copies of the same rotating grant.
2. Record aliases and the provider account/user identity mapping without printing tokens. Preserve external identity so stock resolves the same database rows.
3. With the intended persisted profile destination, use the inherited command for each account, for example:
   ~~~sh
   onwatch codex profile save work --auth-file /auth/codex/work/auth.json
   ~~~
   [Stock argument handling](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/cmd/onwatch/codex_profiles.go#L249) and [profile serialization](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/cmd/onwatch/codex_profiles.go#L672) support this. The CLI profile destination is /data/codex-profiles in a container or the normal user data directory; do not assume a custom ONWATCH_DB_PATH changes the CLI's profile destination.
4. Keep aliases and external IDs stable; the [inherited lookup](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/store/codex_store.go#L1015) reuses the identity's existing row. The name default has special ambient-source behavior: align CODEX_HOME to that same login or choose a nonreserved alias and verify row reuse.
5. Make stock profiles the chosen refresh authority. Stock writes refreshed tokens to its profiles and configured ambient sources, not every imported native home. A later login needs an explicit profile refresh/import workflow.
6. Remove CODEX_AUTH_ROOT only after both accounts poll, retain separate history, honor telemetry toggles, survive restart and refresh without falling back to obsolete native token files.

For Claude in stock Docker, mount the default directory containing .claude and set HOME consistently with the stock file reader. For one ambient Codex login, use a mounted directory and CODEX_HOME. Preserve required write access for refresh persistence; copying a token once is not equivalent to retaining the credential workflow.

For Antigravity, choose either retained account isolation, separate upstream instances/databases, or one selected account with an explicit data conversion. Native upstream plus an installed/authenticated agy is an option; stock Docker can connect to an external language server via base URL/CSRF configuration. Neither automatically replaces multiple account homes inside this image.

**Database rehearsal gate:** use copies or synthetic data, not the production database. Separate accounts before stock can read/write them; retain model-value child rows, reset cycles and session ownership. Verify reads, new writes, a quota reset and rollback. Extra columns being tolerated is insufficient proof of compatibility. A stock trial with Antigravity disabled avoids its writes, but inherited unscoped readers can still expose mixed historical data. Restoring an old auth backup alone is not an OAuth rollback after a refresh token has been consumed: retain the newest grant or re-login. Review P1.6 below.

## 2. Current state machines

### Codex: two credential sources share an alias

~~~text
saved profile "work" starts
  -> native "work" discovered
  -> warning, native skipped
  -> native scan timestamp nevertheless recorded
  -> native auth.json changes
  -> scanner stops agent by alias
  -> native source now starts under "work"
  -> if its external identity differs, name fallback can reuse/relabel old DB row
~~~

The native directory disappearing can also stop/soft-delete the saved-profile account. **S1, F1:** the advertised initial precedence is not maintained through rescans. Target: one explicit source owns each running alias; ignored sources cannot stop or retire it. Removing the competing scanner after stock-profile migration is a viable root fix.

### Antigravity: fork database versus stock reader/writer

~~~text
fork DB: account A/model X active + account B/model X active
  -> stock opens DB, ignores extra account column
  -> latest/history can select rows from either account
  -> stock UPDATE/CLOSE WHERE model_id = X AND cycle_end IS NULL
  -> both accounts' active cycles are modified/closed
~~~

**S1, F2:** this is a write hazard as well as unattributed reads. The same-named fork unique index remains account-scoped. Target: retain scoped code, split databases, or produce a verified single-account conversion before stock writes. New stock rows receive the fork column's default 0 and are reassigned to the fork default on return, so rollback can also change their implied ownership.

### Antigravity residency

~~~text
cap slots full
  -> launch another agy
  -> await ready (nominal timeout 90 seconds)
  -> register new resident
  -> evict older resident
~~~

**S2, F6:** the cap applies after launch/readiness, so live-process count can transiently be cap + 1. A failed startup overlaps the old resident until teardown. Target: reserve/evict before launch and release reservation on every failure. Existing tests observe completed admission, not the peak.

### Backoff and health publication

~~~text
successful poll -> outcome cleared
  -> 429 -> arm skip count -> return without pollFailed
  -> skip several intervals
  -> menubar has no new rate_limit outcome, or retains an older unrelated one
~~~

**S2, F5:** retry reduction works, but the inherited health channel loses the reason. Target: publish the classified failed attempt once, retain that reason during deliberate skips, clear on successful poll.

### Home health

~~~text
fresh Mistral stored snapshot -> capturedAt + per-quota lastUpdatedAt, no snapshotAt
  -> generic Home reads snapshotAt only -> "No data"/provider not updating

other provider rendered fresh -> daemon/network stops responding
  -> fetch failure marks header status dot
  -> no Home rerender -> previous freshness chip/banner can remain frozen
~~~

**S2, F3:** snapshot presence, source age and connection failure are separate facts. Target: normalize a trustworthy stored timestamp across every provider and age cached timestamps on a UI clock even without successful fetches.

### Named Antigravity login isolation - integration uncertainty

~~~text
agy-login(alias) -> named HOME -> starts Secret Service for that login
daemon -> HOME=/home/nonroot -> starts its Secret Service
named agy poll -> changes HOME/XDG_RUNTIME_DIR
              -> inherits daemon DBUS_SESSION_BUS_ADDRESS/GNOME_KEYRING_CONTROL
~~~

Filesystem isolation is implemented. Whether agy selects the intended named OAuth secret on the shared daemon bus is **not established by source review**. Verify two genuinely distinct synthetic/mock identities or explicit opt-in live fixtures before treating multi-account keyring isolation as proven.

## 3. Senior Product Owner perspective

The meaningful differentiator is a unified, container-hosted workflow for multiple Antigravity subscriptions and convenient provider login. Stock Codex/MiniMax account management, stock Anthropic auth and the inherited auto-starter should not be used to justify its maintenance cost.

The fork is already making the right consolidation moves: deleting custom Anthropic rotation/procscan and replacing local CSRF logic removed substantial competing implementations. Continue that strategy with Codex, where upstream supplies the same user outcome through a different credential format.

**S2:** supporting both Codex credential formats without stable source ownership costs more than the discovery convenience is worth. **S2:** the health surface currently promises more certainty than the Mistral timestamp and unverified Antigravity credentials provide. **S3:** a proposed scheduler cannot count as retained product value.

For a one-account native-install persona, upstream is already a strong fit. For the container/multiple-Antigravity persona, an immediate full switch would remove a core workflow. Decide from real provider/account counts, not total fork commit count.

> **PO** favors stock profiles now; **Engineer** requires identity-preserving import and one refresh authority first. Resolution: P4.3 after P1.1/P4.7.
> **PO** favors removing the Go fork; **User** still needs container login and multiple Antigravity accounts. Resolution: keep packaging (carried P4.1), decide P4.2 explicitly, and gate a full switch on P1.6.

Now: establish account needs and repair ownership/health. Next: replace Codex discovery and upstream generic corrections individually. Later: converge to packaging-only customization if Antigravity multi-account is no longer needed or gains upstream support.

Useful operational signals: correct distinct identities per account, successful refresh after restart, number of edited upstream runtime files, failing-poll reason accuracy and time spent resolving the next upstream merge. No arbitrary adoption percentages are asserted.

## 4. Principal Architect & Software Engineer perspective

### Strengths to protect

The one-call route/startup/schema seams are good: [server_overlay.go:9](../internal/web/server_overlay.go#L9), [main_overlay.go:23](../cmd/onwatch/main_overlay.go#L23), and [store.go:1133](../internal/store/store.go#L1133). Fork migrations run after upstream. Credential login services deliberately omit the normal environment/data mount. All three CLI artifacts are now pinned: [Dockerfile.with-user-env:86](../Dockerfile.with-user-env#L86) and [AGY_VERSION:150](../Dockerfile.with-user-env#L150). Stock auth and shared test-home infrastructure have already been restored.

Do not move more existing upstream functions into overlay files. The important exception to the otherwise good separation is a duplicate Antigravity presentation implementation, not a reason for a new abstraction.

### Evidence-ranked findings

| ID / severity | Trigger and consequence | Evidence and minimum correction |
|---|---|---|
| **F1 / S1** | Saved and native Codex sources have the same alias. A native mtime change stops the saved-profile agent; removing the ignored native directory can soft-delete it. A different native external identity can then be attached to the existing name/history. | [ignored load recorded:254](../internal/agent/codex_agent_manager.go#L254), [initial collision:304](../internal/agent/codex_agent_manager.go#L304), [unconditional stop:1011](../internal/agent/codex_agent_manager.go#L1011), [removal:1026](../internal/agent/codex_agent_manager.go#L1026), [name fallback rewrites external ID:1052](../internal/store/codex_store.go#L1052). Fix source ownership or remove native discovery after conversion; P4.7/P4.3. |
| **F2 / S1 migration hazard** | Starting stock with multi-account Antigravity history mixes reads and can modify multiple accounts' cycles in one update. No migration has run in this review. | [fork scoped index:38](../internal/store/fork_schema_overlay.go#L38), [stock close/update](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/store/antigravity_store.go#L343). Use a copy-based data conversion/split; P1.6. |
| **F3 / S2** | Mistral Home always derives missing timestamp as No data even when quotas exist. During complete fetch outage, other Home ages/banner can remain at last render. | [Mistral capturedAt:48](../internal/web/mistral_handlers.go#L48), [generic entry:7446](../internal/web/static/app.js#L7446), [freshness:250](../internal/web/static/fork_overlay.js#L250), [fetch failure:4947](../internal/web/static/app.js#L4947), [reset-only timer:391](../internal/web/static/fork_overlay.js#L391). Add normalized stored timestamps and clock-based age updates; P4.9. Outage behavior is static inference pending browser reproduction. |
| **F4 / S2** | Antigravity detail route uses a second, reduced response builder. IDE reset times/countdowns, rates/projections and top-level promptCredits/monthlyCredits/lowestPool are omitted, while Home/menubar use the richer canonical builder. CLI quotas still reuse the canonical CLI-quota helper. | [route:9947](../internal/web/handlers.go#L9947), [duplicate:25](../internal/web/current_accounts_overlay.go#L25), [canonical:9958](../internal/web/handlers.go#L9958), [IDE reset/rates:10030](../internal/web/handlers.go#L10030). Route through the existing account-aware canonical builder; P4.8. Do not move it out of handlers.go. |
| **F5 / S2** | DeepSeek/Kimi/Moonshot/OpenRouter return from the new 429 branches before publishing pollFailed; their inherited rate-limit reason becomes invisible to the menubar registry. OpenCode already skipped publication upstream, and its new longer backoff retains that limitation. | [DeepSeek:95](../internal/agent/deepseek_agent.go#L95), [Kimi:97](../internal/agent/kimi_agent.go#L97), [Moonshot:95](../internal/agent/moonshot_agent.go#L95), [OpenRouter:101](../internal/agent/openrouter_agent.go#L101), [OpenCode:110](../internal/agent/opencode_agent.go#L110), [classification:263](../internal/agent/poll_status.go#L263), [consumer:369](../internal/web/menubar.go#L369). Keep retry policy and publish the failed attempt; P4.10. |
| **F6 / S2** | A new agy starts before old residents are evicted. The configured count is not a strict live-process/memory ceiling during startup. Current cap tests only observe admission after a fake session already exists. | [launch then admission:368](../internal/api/antigravity_cli.go#L368), [test:18](../internal/api/antigravity_residency_test.go#L18), [claimed bound:500](WITH_USER_ENV.md#L500). Reserve before launch; P4.11. No OOM was reproduced and current peak memory was not measured. |
| **F7 / S2, unverified integration risk** | Named agy HOME differs, but the child shares daemon D-Bus/keyring variables. Correct credential identity per account is not proven by directory existence or session-count tests. | [account HOME:19](../internal/agent/antigravity_overlay.go#L19), [inherited bus:206](../internal/api/antigravity_cli.go#L206), [keyring startup:133](../docker-entrypoint-with-user-env.sh#L133), [credential store documentation:248](WITH_USER_ENV.md#L248). P4.12 should establish actual behavior before changing keyring architecture. No cross-account secret leak is asserted. |
| **F10 / S2** | A temporarily missing Codex root is treated as an empty account directory, so reconciliation can stop/soft-delete every known native account during a mount outage. Antigravity already distinguishes an unavailable root from explicit removals. | [missing root becomes empty:41](../internal/account/account.go#L41), [native reconciliation:993](../internal/agent/codex_agent_manager.go#L993), [existing availability guard:20](../internal/agent/account_reconcile.go#L20). Reuse the availability distinction or remove the scanner; P4.7/P4.3. |
| **F9 / S2 documentation risk; S3 metadata drift** | Resource advice says non-cli source starts no agy and suggests 128M, but both can invoke CLI and AUTH_ROOT forces CLI. Docs also still describe an unpinnable agy bootstrapper although artifacts are now pinned. | [memory advice:489](WITH_USER_ENV.md#L489), [forced CLI:58](../cmd/onwatch/main_overlay.go#L58), [source dispatch:259](../internal/agent/antigravity_agent.go#L259), [stale rule:85](../CLAUDE.md#L85), [pin:150](../Dockerfile.with-user-env#L150). Correct deployment advice and feature inventory; P2.5. |
| **F8 / S3** | Notification dedup keys can change after the sole Codex default row is renamed and the engine restarts: lookup by literal name no longer recognizes ID 1, so a bare quota becomes 1:quota. MiniMax's default-key normalization also differs from stock. This can replay alerts/cooldowns. | [fork namespace:667](../internal/notify/notify.go#L667), [cache:678](../internal/notify/notify.go#L678), [lookup by name:133](../internal/store/provider_account_helpers.go#L133), [inherited rename:954](../internal/store/codex_store.go#L954), [stock namespace](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/notify/notify.go#L657). Define migration-compatible identity before another rewrite; revised P3.5. |

**An earlier assumption to retire:** stock Codex account 1 is not an arbitrary ID accidentally taken by Antigravity. [Upstream migrations seed Codex default explicitly as ID 1](https://github.com/onllm-dev/onWatch/blob/7e2343e9a7e3098ab97e7359f38db111704d9e84/internal/store/store.go#L1248) before the fork runner. The old P3.5 rationale overstated that problem. The actual remaining issue is compatibility across row renaming, cache lifetime and provider-specific key conventions (F8).

**Remaining schema debt:** fork migrations still add/backfill Anthropic account columns even though the runtime no longer scopes Anthropic by account. Existing historical multi-Claude data could therefore already be mixed in the stock-style runtime; this review did not inspect whether such rows exist. P1.4 remains conditional, not silently completed. P4.5 must be driven by actual new upstream schema semantics, not blindly changing DEFAULT 0 to DEFAULT 1.

### Verification coverage and gaps

Existing tests cover native collision warnings, account-scoped store reads/writes, backoff growth/reset, null balance payloads, resident registration and environment allowlisting. They do not establish the source-ownership rescan transitions in F1, peak-before-admission process count in F6, or distinct authenticated keyring identities in F7.

[freshness_test.go:89](../internal/web/freshness_test.go#L89) and [provider_balance_static_test.go:16](../internal/web/provider_balance_static_test.go#L16) include string-presence assertions; those cannot detect missing Mistral timestamp wiring or frozen time during failed fetches. Use one focused behavioral regression for each repaired transition instead of adding more source-string checks.

No tests were run for this review. Required validation for subsequent implementation remains app.sh --smoke and app.sh --test, plus app.sh --integration where relevant and explicit opt-in identity/container checks. Never use actual profile credentials as test fixtures.

Now: F1, migration rehearsal and presentation/health correctness. Next: replace Codex discovery. Later: reduce remaining upstream-file edits where they actually produce conflicts. Keep source changes justified by current usage rather than enforcing an arbitrary zero-diff target.

## 5. Senior Designer / UX perspective

The compact Home answers “which quota/balance is usable right now?” quickly. Real per-account labeling and a rename dialog that leaves credential directory names unchanged are worth protecting. Stock All instead supports diagnosis with charts/insights/collapse controls; either is defensible depending on the main job.

**S2:** contradictory health undermines the dashboard: populated Mistral metrics can sit under a No data warning, while cached “fresh” chips can outlive a failed connection. Fix those before further styling (P4.9). **S2:** the duplicate Antigravity builder removes useful detail-state information (P4.8).

**S3:** Antigravity credential health is always recorded as unverified by [manager:91](../internal/agent/antigravity_agent_manager.go#L91), and [overlay:41](../internal/web/static/fork_overlay.js#L41) suppresses it. The generic missing/unreadable copy is not proof of actionable diagnostics for the only provider accepted by /api/accounts. Narrow the promise or provide observed last-poll/re-login guidance (P4.12/P2.5).

**S3:** picker options are clickable li elements without complete arrow/Enter/focus selection handling; [option creation:108](../internal/web/static/fork_overlay.js#L108), [trigger handlers:220](../internal/web/static/fork_overlay.js#L220). Stock Codex/MiniMax share some of this pattern, so switching is not an accessibility fix. Include a keyboard pass when retaining/refactoring the picker under P4.4.

> **UX** values compact Home and clear account identity; **Architect** values keeping the upstream render path. Resolution: P4.8 removes semantic duplication first; P4.4 keeps only deliberate presentation differences and explicitly preserves or relinquishes upstream overview features.

Now: trustworthy health and detail fields. Next: decide compact overview versus upstream All. Later: polish keyboard flow when that UI is retained.

## 6. End-user perspective - journey walkthrough

**Mara** runs a container on a home server: Claude, work/personal Codex, two Antigravity logins and prepaid API providers. **Dan** runs natively with one Antigravity account and standard Codex profiles.

| Step | Current fork | Switching consequence |
|---|---|---|
| Authenticate providers without installing them on the host | Works through isolated login services. | Stock image needs external acquisition/mounts or retained packaging; container-only workflow loses this step (P4.1). |
| Check work/personal Codex independently | Works normally, with collision hazard when both discovery systems use the same alias. | Stock supports both accounts after import. Fix ownership or convert first (P4.7/P4.3). |
| Check multiple MiniMax subscriptions | Already mostly upstream behavior. | Keep stock account setup/selection/history; verify labels and notifications. |
| Check two Antigravity subscriptions on one page | Fork-only manager/history/picker; live identity isolation remains to be demonstrated. | Stock single instance does not replace this job; keep, split instances, or give it up explicitly (P4.2/P4.12). |
| Choose provider from Home balances/limits | Compact balance rows are useful. | Stock All restores charts/insights; account-wide OpenRouter credits and compact balance summary disappear unless patches stay (P3.1/P3.2). |
| Understand why a card is not updating | Mistral false warning, invisible 429 reason and unverified login diagnostics create friction. | Fix retained fork behavior; upstream has useful but narrower stale indicators (P4.9/P4.10/P4.12). |
| Automatically start Codex rolling windows | Already inherited behavior once toggles are enabled. | No loss. Neither implementation supplies the proposed clock-time scheduler. |
| Leave Home open through an outage | Header notices fetch failure; Home freshness can remain at the previous render. | Requires a clock-driven health refresh if keeping this view (P4.9). |
| Return to stock binary using the current DB | No tested conversion in repository. | Multi-account Antigravity cycles can be mutated across accounts. Rehearse on copies before switching (P1.6). |

For Dan, upstream already serves most of the daily job. For Mara, multiple Antigravity accounts and container login are the real reasons to retain customization. Neither persona benefits from keeping duplicate Codex discovery merely to retain multi-account selection.

## 7. Consolidated cross-perspective roadmap

**Legend:** stable permanent P<phase>.<n> IDs; [ ] open; [x] done; [x] struck through = rejected. S1 correctness/data-loss/security, S2 drift/structural or operational risk, S3 friction/polish. Lenses: PO, Arch, Eng, UX, User. Verdicts: Do, Quick win, Defer: named trigger, Skip unless named condition. needs = hard prerequisite; after = preferred order; none = safe in isolation.

**Open decisions:** D1 remains actual deployed account/provider inventory, not inferred from folders in this checkout. D2 is whether compact Home is a requirement or upstream All is acceptable. D3 is whether native Codex files must remain the refresh authority. No production inventory or migration was performed.

### Phase 1 - Know what must survive, and preserve it

- [ ] **P1.1** Inventory deployed accounts and credential authorities - S2 - PO, Arch, User.
  - **What & where:** record account counts for Claude/Codex/Antigravity, which names correspond to the same provider identity, whether named Anthropic history exists, and which process owns each refresh grant. Record paths/names/IDs only, never token values.
  - **Why:** section 1 migration choices; F1/F2/F7.
  - **Done when:**
    - account counts and required workflows are written down.
    - D1 and D3 are answered.
    - the retained Antigravity strategy is chosen.
  - **Verdict:** Do. **needs:** none.

- [ ] **P1.4** Reconcile old named Anthropic history if it exists - S2 - Arch, User.
  - **What & where:** inspect a backup's anthropic_snapshots/reset_cycles account distribution. Prefer an archive or account-separated copy over deleting production rows; retain children/related sessions. Remove obsolete DDL only in a deliberate migration.
  - **Why:** section 4 residual schema debt; historical P1.2 did not prove data cleanup.
  - **Done when:**
    - historical identity distribution is documented.
    - default Claude views contain only intended history.
    - excluded history is retained separately.
    - rollback is demonstrated.
  - **Verdict:** Skip unless P1.1 finds multiple historical Claude identities. **needs:** P1.1; historical P1.2.

- [ ] **P1.6** Rehearse stock-upstream reads, writes, reset and rollback on isolated data - S1 - Arch, Eng, User.
  - **What & where:** use a copied/synthetic fork DB with two Antigravity accounts sharing a model, plus stock-format Codex/MiniMax fixtures. Select account-separated databases or a single-account conversion and inspect snapshot/model-value/cycle/session ownership.
  - **Why:** F2; section 1 database rehearsal gate.
  - **Done when:**
    - each retained account's latest/history is correct.
    - a stock poll/reset cannot alter another account's rows.
    - fresh stock writes land in the intended account after returning to fork.
    - the pre-migration backup restores cleanly.
  - **Verdict:** Do before full upstream switch. **needs:** P1.1; after P4.2/P4.3 if those features are migrated.

### Phase 2 - Keep the maintenance directions true

- [ ] **P2.5** Correct the current feature/resource/validation descriptions - S2/S3 - PO, Eng, UX.
  - **What & where:** update FORK_FEATURES and CLAUDE for pinned agy artifacts and mounted OAuth support in stock Docker; correct WITH_USER_ENV's both/AUTH_ROOT CLI and memory advice; distinguish Antigravity unverified health from delivered diagnostics; replace the old uncommitted-status narrative with current evidence.
  - **Why:** F9 and section 5; historical P2.4 is preserved but does not guarantee future docs stay current.
  - **Done when:**
    - docs name all three pinned CLIs correctly.
    - memory advice covers cli/both/forced CLI.
    - auth acquisition and tracking are distinguished.
    - validation claims identify what actually ran.
    - health promises match the current provider.
  - **Verdict:** Quick win. **needs:** none.

### Phase 3 - Offer generic corrections upstream, one coherent change at a time

These entries remain plans. No issue, PR, message, push or tracker write was created during this review.

- [ ] **P3.1** Propose honest empty-balance payloads and required timestamp fields - S2 - PO, UX, Eng.
  - **What & where:** DeepSeek/Moonshot/OpenRouter response defaults, snapshotAvailable/snapshotAt contract and UI rendering. Keep optional compact Home layout separate.
  - **Why:** section 1 parity matrix; stock browser has some empty-state protection but API defaults remain ambiguous.
  - **Done when:**
    - missing amounts/timestamps are absent or null.
    - a real zero remains distinguishable.
    - provider pages/All/API consumers agree.
    - an upstream adoption decision is recorded and the local patch is dropped only after equivalent behavior lands.
  - **Verdict:** Do if these providers/API clients are used. **needs:** none.

- [ ] **P3.2** Propose OpenRouter account credits as a separate capability - S2 - PO, Eng.
  - **What & where:** openrouter_client/types/store and credit-column migration; keep key usage and management-key account totals distinct. Do not relabel key remaining limit as purchased balance.
  - **Why:** section 1 OpenRouter parity and official API references.
  - **Done when:**
    - /key succeeds independently of optional /credits.
    - management-key limitations are clear.
    - absent totals stay unavailable.
    - upstream's migration retains old rows.
    - local storage/UI additions are removed only after equivalent upstream adoption.
  - **Verdict:** Do if account credits are required; otherwise accept stock key usage. **needs:** none.

- [ ] **P3.3** Finish the procfs portability delta; CSRF portion is already upstream - S2 - Arch, Eng.
  - **What & where:** antigravity_procports.go and discoverPortsLinux. Upstream already supplies CSRF arguments/token propagation; do not reopen that implementation.
  - **Why:** section 1 Antigravity matrix; slim image lacks ss/netstat.
  - **Done when:**
    - port discovery works without external tools and with child listener processes.
    - fallback behavior remains.
    - an upstream adoption decision is recorded.
    - no custom CSRF layer reappears.
  - **Verdict:** Quick win for upstream proposal; keep locally for slim container use. **needs:** none.

- [ ] **P3.4** Separate the small portable corrections - S3 - Eng.
  - **What & where:** setup EOF handling, profile-directory error handling, app.sh integration flag, .gitattributes and SQLite immediate transactions. Present locking semantics independently with contention evidence rather than bundling unrelated changes.
  - **Why:** section 1 small-fix parity; these are independent of multi-account architecture.
  - **Done when:**
    - each retained patch has a reason and focused check.
    - each proposed upstream change stands alone.
    - accepted patches are removed locally after merge.
  - **Verdict:** Quick win when touching these areas. **needs:** none; backoff health belongs to P4.10.

- [ ] **P3.5** Establish stable notification dedup semantics before upstreaming a default-ID fix - S3 - Arch, Eng, User.
  - **What & where:** notificationQuotaKey/defaultAccountID and literal-name lookup. Preserve historical Codex ID-1 behavior across first-profile rename/restart; decide MiniMax legacy key migration explicitly. Reassess the old “upstream default ID is wrong” premise.
  - **Why:** F8; upstream schema deliberately seeds Codex ID 1.
  - **Done when:**
    - before/after restart keys remain compatible after renaming default.
    - MiniMax/fork account transitions do not accidentally replay cooldowns.
    - cache invalidation matches account lifecycle.
    - an evidence-based upstream proposal/keep decision replaces the old premise.
  - **Verdict:** Quick win before notification changes or a full switch. **needs:** none.

### Phase 4 - Keep only required extensions, repair their boundaries

- [ ] **P4.2** Decide the Antigravity multi-account deployment - S2 - PO, Arch, User.
  - **What & where:** ANTIGRAVITY_AUTH_ROOT, account manager, account-scoped store/tracker/API. Choose retain, separate upstream instances/databases, or one-account conversion; adapt HOME/keyring/source settings together.
  - **Why:** section 1 true differentiation; F2/F7.
  - **Done when:**
    - required simultaneous identities are recorded.
    - retained identities continue polling after restart.
    - chosen data/history strategy is demonstrated.
    - Prometheus selectors and account-label changes are addressed.
    - unsupported single-instance upstream parity is not claimed.
  - **Verdict:** Do the decision now; keep the code while multiple accounts remain required. **needs:** P1.1; verify retained design through P4.12.

- [ ] **P4.3** Replace native Codex discovery with upstream saved profiles where acceptable - S2 - PO, Arch, User.
  - **What & where:** import native auth files via the existing CLI; retain external identity/aliases/profile destination; remove CODEX_AUTH_ROOT scanner and native write-back helpers only after migration. This works for multiple accounts too, correcting the prior one-account-only framing.
  - **Why:** section 1 Codex migration; F1.
  - **Done when:**
    - all intended accounts poll/refresh from stock profiles.
    - row IDs/history/telemetry survive.
    - default alias behavior is verified.
    - one refresh authority exists.
    - manager/credential-source diffs attributable to native homes disappear.
  - **Verdict:** Do unless automatic native write-back is required. **needs:** P1.1; after P4.7 unless scanner is removed directly during isolated migration.

- [ ] **P4.4** Choose and contain the Home presentation delta - S2/S3 - Arch, UX, User.
  - **What & where:** app.js/dashboard_tabs/templates plus fork_overlay.js/css. Record D2; restore upstream All if acceptable, or keep compact Home through small hooks without copying canonical provider semantics. Include picker keyboard navigation if retained.
  - **Why:** section 5; app.js still carries +320/-153 and changes overview functionality.
  - **Done when:**
    - accepted chart/insight/collapse differences are documented.
    - selected accounts navigate correctly.
    - inherited provider behavior remains canonical.
    - retained picker can be opened/selected/closed by keyboard.
    - upstream-file edits shrink without moving existing functions into overlay files.
  - **Verdict:** Defer structural UI refactor until next overlapping upstream UI change; fix F3/F4 first. **needs:** none; after P4.8/P4.9.

- [ ] **P4.5** Reconcile schema only when upstream adds equivalent Antigravity account columns - S2 - Arch.
  - **What & where:** fork_schema_overlay.go and relevant upstream migrations. Compare actual account-ID ownership, backfill and unique-index semantics; do not assume changing a SQL DEFAULT aligns existing rows.
  - **Why:** F2 and section 4 residual schema debt.
  - **Done when:**
    - both existing and fresh DB fixtures have correct account mappings.
    - scoped active-cycle uniqueness holds.
    - reopening is idempotent.
    - fork-to-stock-to-fork behavior is verified.
  - **Verdict:** Defer until upstream adds equivalent account semantics or P1.6 requires conversion. **needs:** the chosen P4.2 strategy.

- [x] **P4.7** Make Codex alias ownership consistent across initial load, rescan and removal - S1 - Eng, User.
  - **Done (2026-10-30):** native discovery now lives in `internal/agent/codex_agent_manager_overlay.go` (the `Native` field is gone from the upstream profile struct) and only the profile-scan hook remains in the upstream file. The scanner no longer treats an unavailable root as "every account deleted", a deleted legacy profile is not shielded by an ignored native home, and a native login whose external identity differs from the alias's existing row is refused. A row with no identity yet (upstream's seeded Codex `default`) stays claimable, so the container's `/auth/codex/default` login still polls; a refused login is re-checked only after its `auth.json` changes. Regression: `codex_agent_manager_test.go` (`IgnoredNativeCollisionCannotReplaceOrRemoveLegacy`, `SourceRemovalAffectsOnlyItsOwner`). `./app.sh --test` and `./app.sh --smoke` pass.
  - **What & where:** loadAndStartNativeAccount, scanForNativeAccountChanges and stop/removal paths in codex_agent_manager.go. Track/check the active source or reject collisions before recording scanner ownership; do not let an ignored native source stop/retire a legacy owner.
  - **Why:** F1/F10.
  - **Done when:**
    - updating ignored native auth leaves legacy agent/DB identity unchanged.
    - removing ignored native dir leaves it active.
    - active native and legacy owners still reload/delete correctly.
    - a different external identity cannot silently inherit the other's history.
    - a missing/unmounted native root does not retire all accounts, while actual directory removal still retires only its owned source.
  - **Verdict:** Do; alternatively satisfy by removing native discovery via verified P4.3. **needs:** none.

- [x] **P4.8** Remove the duplicate Antigravity response implementation - S2 - Arch, Eng, UX.
  - **Done (2026-10-30):** `/api/current?provider=antigravity` calls the canonical account-aware builder with a one-line change in `handlers.go`; the reduced builder is deleted from `current_accounts_overlay.go`. Regression: `TestCurrentAntigravityUsesCanonicalAccountAwarePayload` asserts `snapshotAt`, credit fields, `lowestPool`, `account`, and reset fields on the IDE quota.
  - **What & where:** currentAntigravity should call the existing account-aware buildAntigravityCurrent/buildAntigravityCurrentOK; remove the reduced builder from current_accounts_overlay.go. Preserve the canonical function in handlers.go.
  - **Why:** F4.
  - **Done when:**
    - detail and Home/menubar expose consistent reset fields/credit fields/lowestPool for the same account.
    - IDE rate/projection behavior is retained or explicitly account-scoped.
    - CLI display mode behavior is unchanged.
    - missing/read-error behavior is tested.
  - **Verdict:** Quick win. **needs:** none.

- [x] **P4.9** Make Home freshness correct for every provider and through outages - S2 - Eng, UX, User.
  - **Done (2026-10-30):** Mistral current responses now expose `snapshotAt` from the stored capture time, so populated metrics no longer sit under a "No data" warning. The minute tick updates freshness chips and the banner in place (no re-render, so focus/hover/scroll survive an outage) and folds the reset countdown into the same pass. Account chips on grouped cards carry their own stored time, so each ages independently and the oldest decides the card. Regression: `TestMistralReporting`, `TestHomepageHealthAgesInPlaceWithoutRerendering`, `TestHomepageHealthAgesEveryAccountChip`.
  - **What & where:** Mistral timestamp adapter/handler plus generic entry building, providerFreshness/banner and a UI-only aging pass. Distinguish stored timestamp from response time and quota/source age.
  - **Why:** F3.
  - **Done when:**
    - fresh Mistral data is not No data.
    - absent snapshots remain No data.
    - stale quotas are not made fresh by unrelated new data.
    - after fetches fail, existing cards/banner age without a reload.
    - countdowns and account grouping continue working.
  - **Verdict:** Do if keeping Home. **needs:** none.

- [x] **P4.10** Publish failed poll outcomes when applying generic 429 backoff - S2 - Eng, User.
  - **Done (2026-10-30):** the five branches share one overlay helper, `pollBackoff.rateLimited`, which arms the skip window and publishes the classified failure, so the first 429 sets `rate_limit`, skipped cycles keep that reason, and a later success clears it. Regression: `TestGenericRateLimitBackoffPublishesHealthForHTTPProviders` plus the OpenCode backoff test.
  - **What & where:** five rate-limit branches should publish classified failure before returning; leave skip intervals and success reset semantics intact.
  - **Why:** F5.
  - **Done when:**
    - first 429 sets rate_limit outcome.
    - skipped cycles issue no requests.
    - last failure reason remains meaningful.
    - successful poll clears reason and backoff.
    - all five providers share the checked behavior.
  - **Verdict:** Quick win. **needs:** none.

- [x] **P4.11** Reserve resident capacity before launching agy - S2 - Arch, Eng.
  - **Done (2026-10-30):** `launchReservedSession` (in `antigravity_residency_overlay.go`) admits (and therefore evicts) before the process starts and releases the reservation when launch or readiness fails. No peak of live processes above the configured cap exists during startup. Regression: `TestAgyAdmissionPrecedesLaunchAndFailureReleasesReservation` covers both failure points. The measured-versus-bound wording in `docs/WITH_USER_ENV.md` is untouched and still needs P2.5.
  - **What & where:** admission/launch/readiness/teardown in antigravity_cli.go; keep context cancellation and eviction lock ordering correct.
  - **Why:** F6 and resource-budget claims.
  - **Done when:**
    - live launched processes never exceed configured cap, including delayed/failed readiness.
    - failed launch releases its reservation.
    - Stop/eviction releases/reaps resources.
    - a launch-peak check complements post-admission tests.
    - docs distinguish measured values from bounds.
  - **Verdict:** Do if multiple Antigravity accounts remain. **needs:** none.

- [ ] **P4.12** Establish end-to-end Antigravity account identity and honest recovery health - S2 - Arch, Eng, UX, User.
  - **What & where:** accountCLIEnv, inherited D-Bus/keyring environment and login/daemon lifecycle. First reproduce with isolated fake/explicit opt-in account fixtures; change Secret Service layout only if required by observed agy behavior.
  - **Why:** F7 and section 5 unverified-health limitation.
  - **Done when:**
    - two distinct account logins repeatedly yield their own identities/quotas after restart and eviction.
    - one invalid login cannot reuse a sibling's identity.
    - intended keyring persistence is demonstrated.
    - failures surface actionable last-poll/login guidance instead of implied verified credentials.
  - **Verdict:** Do before relying on multi-account isolation. **needs:** none; live checks require explicit opt-in and isolated account state.

### Carried resolved decisions from the 2026-10-12 review

The following nine entries are preserved verbatim apart from the appended “carried” marker on each first line. Their old “working tree, uncommitted” text, line anchors, commands and test claims are historical, not instructions or claims about the current checkout. The changes now live in Git history (including a3e9cc2); this review reverified the current source where described above. P1.1/P1.4 remain unresolved, P1.2 left residual schema, P1.3 still lacks new live validation, P2.3's server also has legitimate fork route/cache hooks, and P2.4 does not imply current docs are drift-free.

- [x] **P1.2** (working tree, uncommitted) Revert cluster A and the Anthropic half of cluster B to upstream · **S2** · PO, Arch, Eng · carried
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

- [x] **P1.3** (working tree, uncommitted; shell syntax and dispatch test checked, image not built) Re-plumb the container for stock Anthropic auth · **S1** · Arch, UX, User · carried
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

- [x] **P1.5** (working tree, uncommitted) Restore the stock Anthropic docs · **S3** · PO · carried
  - *What & where:* restore upstream's "Anthropic Rate Limit Bypass" section in CLAUDE.md and
    the README feature line and 429 troubleshooting paragraph. Remove FORK_FEATURES §5.
  - *Why:* CLAUDE.md is loaded into every agent session; a stale description of the lock would
    misdirect future work.
  - *Done when:*
    - `git diff upstream/main -- CLAUDE.md README.md` contains no Anthropic lines
  - *Verdict:* **Do**
  - *after:* P1.3

- [x] **P2.1** (working tree, uncommitted) Repair `AGENTS.md` · **S2** · Eng · carried
  - *What & where:* restore the symlink (`git checkout upstream/main -- AGENTS.md`) and move the
    "Test environment safety" rules into CLAUDE.md, or into a new non-symlink file that
    CLAUDE.md references.
  - *Why:* §2 defect 3.
  - *Done when:*
    - `git cat-file -p HEAD:AGENTS.md` prints exactly `CLAUDE.md`
    - the safety rules are readable from a Linux checkout
  - *Verdict:* **Quick win**

- [x] **P2.2** (working tree, uncommitted) Replace `internal/testenv` with upstream `testhome` · **S2** · Arch, Eng · carried
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

- [x] **P2.3** (working tree, uncommitted) Single source for the Windows bind default · **S3** · Eng · carried
  - *What & where:* keep the default in [config.go:597](../internal/config/config.go#L597) only.
    Delete the override at [main.go:728-730](../cmd/onwatch/main.go#L728-L730) and
    `forkDefaultServerHost` in `server_overlay.go`. Restore upstream `server.go`.
  - *Why:* §2 defect 6.
  - *Done when:*
    - `SYNTRACK_HOST=0.0.0.0` on Windows binds `0.0.0.0`
    - with no host variable set, Windows binds `127.0.0.1`
    - `server.go` matches upstream
  - *Verdict:* **Quick win**

- [x] **P2.4** (working tree, uncommitted) Make the fork's docs and backlog true · **S3** · PO · carried
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

- [x] **P4.1** (decision recorded) Keep the `with-user-env` image · **S3** · PO, User · carried
  - *What & where:* no change beyond P1.3. It is additive and conflict-free, and it is the
    reason the fork exists.
  - *Why:* §3 "what's genuinely good".
  - *Done when:*
    - this review's §1 cluster C still describes the image after P1.3
  - *Verdict:* **Do** (keep)

- [x] ~~**P4.6** No-rotate-on-429 guard for native installs~~ container-only (assumed; reopen if onWatch ever runs natively beside Claude Code) · **S2** · Eng, User · carried
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


## 8. Summary scorecard

Grade bands: A = solid core, no open S1, S2s known/planned; B = solid core with known unfixed S2s; C = a core loop breaks somewhere or an open S1; D/F = does not do its job. Grades concern the remaining fork delta and migration safety, not upstream's entire product.

| Dimension | Grade | Rationale |
|---|---|---|
| Domain model | C | Account-scoped Antigravity persistence is valuable, but competing Codex sources can attach different identities to one alias/history. |
| Engineering | C | Source-ownership correctness issue plus duplicated presentation semantics; useful focused tests miss the important transitions. |
| Integration | C | Stock migration can mutate multiple Antigravity accounts' cycles; credential/layout compatibility needs an explicit rehearsal. |
| Product completeness | B | Core daily monitoring exists; unique container/multi-account needs remain, while many claimed differentiators are upstream already. |
| UX/UI | B | Useful compact Home/account labels; inconsistent freshness, omitted detail fields and incomplete recovery/keyboard behavior remain. |
| Maintainability | B | Anthropic/CSRF/test-home convergence is a major improvement; remaining native discovery, provider presentation and schema semantics still need care. |

**Single next investment:** make Codex account ownership and profile migration demonstrably safe, then remove the native-home scanner if automatic write-back is not required. This deletes a competing lifecycle while retaining upstream's multi-account user experience. Keep the login packaging and required Antigravity isolation until their independent replacement gates are satisfied.

**Review deliverable:** 28 permanent roadmap items: 19 open, 8 historically done and 1 historically rejected. The carried decisions retain their original text; new/revised open items explain the current source and migration gates. No implementation, production migration, tracker sync or upstream publication was performed.
