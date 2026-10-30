# onWatch Fork Features & Upstream Delta

What this fork carries on top of upstream (`onllm-dev/onWatch`). Every section names files that exist. Anything not in this document is stock upstream. For the review that produced this list and the plan for shrinking it, see [FORK_DELTA_MULTI_ANGLE_REVIEW.md](FORK_DELTA_MULTI_ANGLE_REVIEW.md).

Stock behaviour the fork deliberately does **not** change: Anthropic authentication (OAuth refresh, rate-limit handling, credential files), `provider_accounts` and Codex/MiniMax multi-account, OpenRouter as a provider, the Codex auto quota-starter, and `internal/testutil/testhome`.

---

## 1. `with-user-env` container image

Upstream's container is a distroless image with API keys only. Providers that need an interactive OAuth login (Claude Code, Codex, Antigravity) cannot log in there.

- `Dockerfile.with-user-env` - Debian runtime with `codex` and `claude` pinned by version and SHA-256 (reduced to one native binary each) and `agy` fetched by its bootstrapper.
- `docker-compose.override-with-user-env.yml` - one named volume per credential store and one-shot `agy-login` / `codex-login` / `claude-login` services that get no `.env` and no `/data`.
- `docker-entrypoint-with-user-env.sh` - allowlists the exact login commands (anything else exits 64), validates `ONWATCH_LOGIN_ACCOUNT`, starts D-Bus and gnome-keyring, and symlinks the daemon's `$HOME/.claude` to the default Claude login so stock Anthropic auth finds it.
- `scripts/test-entrypoint-dispatch.sh`, `scripts/test-with-user-env-image.sh` - dispatch and image checks.
- `mise.toml` - build and push tasks for the image.
- Docs: [WITH_USER_ENV.md](WITH_USER_ENV.md).

## 2. Multi-account for Antigravity (and native Codex discovery)

Upstream already supports multiple Codex and MiniMax accounts. The fork adds:

- **Antigravity**: `ANTIGRAVITY_AUTH_ROOT=<root>` with one folder per account, a per-account agent manager (`internal/agent/antigravity_agent_manager.go`), `account_id` on `antigravity_snapshots` and `antigravity_reset_cycles` (`internal/store/fork_schema_overlay.go`, `internal/store/antigravity_store.go`), and account-scoped API/dashboard views.
- **Codex native homes**: `CODEX_AUTH_ROOT=<root>` discovers `<root>/<alias>/auth.json` (`internal/agent/codex_agent_manager.go`, `internal/api/codex_credentials.go`).
- **Shared plumbing**: `internal/account/`, `internal/agent/account_reconcile.go`, `internal/store/provider_account_helpers.go`, `cmd/onwatch/main_overlay.go` (startup hooks), `internal/web/accounts_overlay.go` (`/api/accounts`), account identity in notifications (`internal/notify/notify.go`) and metrics (`internal/metrics/metrics.go`).
- Docs: [MULTI_ACCOUNT.md](MULTI_ACCOUNT.md).

Anthropic is **not** multi-account in this fork. Only the default Claude login is polled.

## 3. Antigravity CLI fixes

- `internal/api/antigravity_procports.go` - resolves listening ports from `/proc`, needed in slim container images.
- `internal/api/antigravity_cli.go`, `internal/api/antigravity_residency_overlay.go`, `internal/agent/antigravity_agent.go`, `internal/agent/antigravity_overlay.go` - bounded resident `agy` sessions (`ANTIGRAVITY_MAX_RESIDENT_SESSIONS`), launch backoff after consecutive CLI failures, per-account `HOME` and `XDG_RUNTIME_DIR`. The cap is reserved before a process starts and released if startup fails, so readiness latency cannot put a second `agy` beside the session it is replacing.

## 4. Provider correctness

- **No-data states**: DeepSeek and Moonshot return `null` amounts and `snapshotAvailable: false` instead of a healthy-looking zero before the first poll (`internal/web/deepseek_handlers.go`, `moonshot_handlers.go`), with a neutral `unknown` badge in `style.css`.
- **OpenRouter**: uses `/api/v1/key` and adds account-wide credits from `/api/v1/credits` when the key allows it (`internal/api/openrouter_client.go`, `openrouter_types.go`, `internal/store/openrouter_store.go`).
- **Rate-limit backoff**: `internal/agent/backoff.go` skips a growing, capped number of poll cycles after a 429 for DeepSeek, Kimi, Moonshot, OpenRouter and OpenCode Go. It is deterministic; there is no jitter. `internal/agent/backoff_overlay.go` publishes the classified `rate_limit` outcome for the failed attempt, so the menubar explains the pause instead of showing an old or missing reason; the reason persists through the skipped cycles and clears on the next successful poll.

## 5. Dashboard

- `internal/web/static/fork_overlay.js`, `fork_overlay.css`, linked from `templates/layout.html` - account picker and rename dialog, provider freshness banner, reset countdowns, balanced cards. Freshness chips and the banner age in place from the stored snapshot timestamp once a minute, so they stay truthful when polling stops without re-rendering the cards (focus, hover and scroll position survive an outage).
- `internal/web/static/app.js`, `internal/web/dashboard_tabs.go`, `templates/dashboard.html` - the combined view is the first tab, labelled "Home" and titled "Harness Overview".

## 6. Small fixes and repo hygiene

- SQLite opens with `_txlock=immediate` (`internal/store/store.go`) to avoid `SQLITE_BUSY_SNAPSHOT` in read-then-write transactions.
- `cmd/onwatch/setup.go` - interactive prompts stop on EOF instead of looping.
- `cmd/onwatch/codex_profiles.go` - a profiles path that is not a directory is an error.
- `internal/config/config.go` - on Windows the server binds `127.0.0.1` unless `ONWATCH_HOST` is set.
- `app.sh --integration` and `make integration`.
- Tests bind to `127.0.0.1` and use `internal/testutil/testhome` for home isolation.
- `.gitattributes` forces LF for shell, Docker, YAML and TOML files.
- `backlog/` holds the Backlog.md tasks for this work.
- Fork architecture rules: [FORK_ARCHITECTURE_GUIDELINES.md](FORK_ARCHITECTURE_GUIDELINES.md).

## Proposed, not built

- [Scheduled quota primer](plans/QUOTA_WINDOW_PRIMER_MULTI_ANGLE_REVIEW.md) - a plan to send a tiny request at a chosen time. No code exists. It overlaps upstream's Codex auto quota-starter.
