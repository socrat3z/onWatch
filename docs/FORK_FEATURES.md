# onWatch Fork Features Specification & Upstream Delta

This document provides a comprehensive catalog of all features, enhancements, architecture changes, and behavioral differences introduced in this repository on top of upstream (`onllm-dev/onWatch`).

---

## 1. Multi-Account Management per Provider

Upstream supports at most one account per provider (single API key or single CLI session). This fork introduces full multi-account architecture, allowing users to track, monitor, and switch between multiple active accounts for any supported AI provider.

### Core Architecture & Components
- **Package**: `internal/account/`
  - `Account` struct: Represents a provider account (`ID`, `Provider`, `Name`, `AuthType`, `IsDefault`, `Status`, `Metadata`, timestamps).
  - `AccountStore` interface: Methods for listing, resolving defaults, switching, soft-deleting, and updating accounts.
  - `anthropic_source.go`, `antigravity_source.go`: Provider-specific detectors that list account directories without loading credentials into the account model.
- **Dynamic Reconciler**: `internal/agent/account_reconcile.go`
  - Periodically reconciles detected system accounts with persistent store entries.
  - Detects newly added CLI profiles without requiring service restarts.
  - Soft-deletes accounts that are no longer present or failed initial startup (`task-6`).
- **Provider Agent Managers**:
  - `internal/agent/anthropic_agent_manager.go`
  - `internal/agent/antigravity_agent_manager.go`
  - `internal/agent/codex_agent_manager.go`
  - Instead of running a single global polling agent per provider, agent managers supervise an independent sub-agent per configured account, isolating rate limits, token rotations, and quota polling cycles.
  - `cmd/onwatch/main_overlay.go` owns their downstream startup, notifier, and
    registry integration behind three stable hooks in the upstream entrypoint.

### Database Schema Extensions
- **Files**:
  - `internal/store/fork_schema_overlay.go`: Idempotent fork-only DDL, indexes,
    and legacy account backfills, run after upstream migrations.
  - `internal/store/provider_account_helpers.go`: Account lookup and mutation helpers.
  - `internal/store/store.go`: One post-upstream migration hook only.
  - `internal/store/migration.go`: Account-aware cycle recalculation.
- **Table**: `provider_accounts`
  - `id INTEGER PRIMARY KEY AUTOINCREMENT`
  - `provider TEXT NOT NULL`
  - `name TEXT NOT NULL`
  - `auth_type TEXT NOT NULL`
  - `is_default INTEGER NOT NULL DEFAULT 0`
  - `status TEXT NOT NULL DEFAULT 'active'`
  - `metadata TEXT`
  - `created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`
  - `updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`
  - `deleted_at TIMESTAMP`
- **Account-Scoped Queries**:
  - `GetSnapshotsByAccount(provider string, accountID int64, limit int)`
  - `ResolveDefaultProviderAccount(provider string)`
  - `ListProviderAccounts(provider string)`
  - `SwitchActiveAccount(provider string, accountID int64)`
  - `RenameProviderAccount(provider string, accountID int64, newName string)`
  - Push account predicates into SQL range and history queries (`task-2`).

### Codex Multi-Profile CLI & Session Management
- **Files**: `cmd/onwatch/codex_profiles.go`, `cmd/onwatch/codex_profiles_test.go`
- **Commands**:
  - `onwatch codex profile list`: Lists active Codex CLI profiles with bound account IDs.
  - `onwatch codex profile switch <name>`: Switches the active Codex CLI session.
  - `onwatch codex profile refresh`: Refreshes OAuth credentials for named profiles.
  - Warning and collision detection between native and legacy profiles (`task-12`).

### Web UI & HTTP API
- **Route overlay**: `internal/web/server_overlay.go` owns downstream route
  registration through one hook in the upstream server constructor.
- **Current-data overlay**: `internal/web/current_accounts_overlay.go` owns
  account-scoped session fallback and Antigravity account response assembly.
- **Endpoints**:
  - `GET /api/accounts?provider=<provider>`: List accounts for provider.
  - `POST /api/accounts/switch`: Switch active account.
  - `POST /api/accounts/rename`: Rename an account label.
  - `POST /api/accounts/delete`: Soft-delete an account.
  - Surface account health, error status, and quotas in API responses (`task-8`).
- **Dashboard UI**:
  - Header account picker dropdown (`task-3`, `task-9`).
  - In-app account rename modal dialog (`task-10`).
  - Account badges and indicators on homepage provider cards.
  - Account identity in notification deduplication and alert bodies (`task-11`).

---

## 2. Docker `with-user-env` Container Environment

Upstream containerization only supported direct API keys passed as environment variables. Providers requiring CLI authentication (Claude Code, Windsurf/Antigravity, Codex) could not run inside Docker.

### Capabilities & Components
- **Dockerfile**: `Dockerfile.with-user-env`
  - Multi-stage build installing Node.js, Claude Code CLI (`@anthropic-ai/claude-code`), Python runtime, and required dependencies alongside the native `onwatch` Go binary.
- **Compose & Entrypoint**:
  - `docker-compose.override-with-user-env.yml`: Mounts host user directories (`~/.claude`, `~/.codex`, `~/.windsurf`, `~/.local`) into container volumes safely.
  - `docker-entrypoint-with-user-env.sh`: Safely translates host UID/GID to container runtime user to prevent permissions clobbering.
- **Security Hardening**:
  - Path traversal validation on login aliases (`task-4`).
  - Strict environment variable allowlist for spawned CLI subprocesses (`task-5`).
  - Runtime directory creation (`XDG_RUNTIME_DIR`) before launching background tools (`task-16`).
- **Documentation & Verification**:
  - `docs/WITH_USER_ENV.md`
  - Automated test harnesses: `scripts/test-with-user-env-image.sh`, `scripts/test-entrypoint-dispatch.sh`.

---

## 3. Intelligent Rate Limit Backoff & Escalation

Upstream polls providers at fixed intervals. When encountering HTTP 429 (Too Many Requests), upstream could repeatedly retry or enter failure loops that worsen rate limits.

### Components
- **File**: `internal/agent/backoff.go`, `internal/agent/backoff_test.go`
- **Backoff Algorithm**:
  - Exponential backoff with Full Jitter: $T_{\text{sleep}} = \text{random}(0, \min(T_{\text{max}}, T_{\text{base}} \times 2^{\text{attempt}}))$.
  - Prevents thundering herds across parallel polling loops.
- **Provider Integrations**:
  - `AntigravityAgent`: CLI backoff mechanism preventing resident `agy` processes from overwhelming the system or colliding on the port (`task-21`).
  - `DeepSeekAgent`, `KimiAgent`, `MoonshotAgent`, `OpenCodeAgent`, `OpenRouterAgent`: Wired to rate limit backoff handlers.
- **Sustained 429 Escalation**:
  - Files: `internal/agent/anthropic_ratelimit_escalation_test.go`
  - Instead of retrying indefinitely, detects sustained 429 saturation, temporarily pauses polling, notifies the operator, and resets gracefully upon cooldown.

---

## 4. Quota Window Primer

- **File**: `docs/plans/QUOTA_WINDOW_PRIMER_MULTI_ANGLE_REVIEW.md`
- **Feature**:
  - Proactively queries and warms provider quota endpoints immediately before and after scheduled weekly or hourly reset windows.
  - Ensures dashboard limits reset countdown timers display immediate, accurate data rather than stale zero-values following quota boundaries.

---

## 5. Credential Concurrency & Advisory File Locking

When both `onwatch` daemon and the developer's interactive CLI (e.g. `claude`) run simultaneously, both may attempt OAuth token refresh at the same time, causing token revocation and `invalid_grant` errors.

### Components
- **Files**:
  - `internal/api/anthropic_lock.go`
  - `internal/api/anthropic_lock_unix.go` (`syscall.Flock`)
  - `internal/api/anthropic_lock_windows.go` (`windows.LockFileEx`)
- **Behavior**:
  - Acquire non-blocking advisory file lock prior to refreshing credentials.
  - Safe credential rotation with atomic write-and-replace (`anthropic_rotation_test.go`).
  - If locked by active CLI session, skip refresh and adopt newly written credentials.
  - Diagnostics logging explaining why refresh was skipped without flooding logs (`anthropic_refresh_diag_test.go`).
- **Process Detection**:
  - `internal/agent/anthropic_cc_detect.go`: Reports exact matched command lines without false positives.

---

## 6. Antigravity CLI Subprocess & Port Lifecycle Management

- **Files**: `internal/api/antigravity_procports.go`, `internal/api/antigravity_procports_test.go`
- **Feature**:
  - Tracks background `agy` resident language server processes and allocated ports (`task-3`).
  - Bounds resident process count and measures multi-account memory consumption.
  - Cleans up orphan child processes during shutdown or reload.

---

## 7. OpenRouter Provider Support

- **Files**:
  - `internal/api/openrouter_client.go`, `internal/api/openrouter_types.go`, `internal/api/openrouter_client_test.go`
  - `internal/store/openrouter_store.go`, `internal/store/openrouter_balance_test.go`
  - `internal/agent/openrouter_agent.go`
  - `internal/web/provider_balance_static_test.go`
- **Feature**:
  - Adds OpenRouter as a supported balance-tracking and usage provider.

---

## 8. Dashboard UI/UX Enhancements

- **Files**:
  - `internal/web/static/app.js`
  - `internal/web/static/style.css`
  - `internal/web/templates/dashboard.html`
  - `internal/web/freshness_test.go`
- **Enhancements**:
  - **Freshness Banner**: Live indicator showing snapshot age and polling status on homepage.
  - **Limits Reset Countdowns**: Live ticking countdown timers to quota resets.
  - **Balanced Provider Cards**: Symmetrical card layout and graceful handling of empty/no-data states.
  - **All Providers View**: Reorganized CSS grid layout with improved responsiveness.
  - **Modal Dialogs**: Accessible, keyboard-navigable dialogs for account switching and renaming.

---

## 9. Hermetic Test Environment Isolation

- **Files**: `internal/testenv/userenv.go`, `internal/testenv/userenv_test.go`
- **Feature**:
  - Redirects `HOME`, `USERPROFILE`, `LOCALAPPDATA`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` during tests.
  - Guarantees tests never read, write, or corrupt developer user profiles or live credentials on any OS.
