# Agent Multi-Profile Design

## Status

Proposed - not yet implemented.

## Scope

This document proposes a consistent way for the optional `with-user-env` image
to hold multiple authenticated accounts for the bundled Antigravity, Codex, and
Claude Code CLIs.

Here, an **account** means one authenticated provider identity. The term
**Docker Compose profile** remains reserved for conditionally enabling services,
such as the existing `login` profile.

The design has two distinct responsibilities:

1. The container stores and selects isolated credential state for named accounts.
2. onWatch discovers those accounts, polls each one, and stores account-scoped
   telemetry.

Container changes alone can support logging into and selecting multiple
accounts, but they cannot provide simultaneous multi-account tracking without
the application changes described below.

## Findings

### Profiles must be runtime state

Profiles should not be represented by Docker build arguments, image targets, or
separate images. Authentication state is mutable and secret; baking it into an
image would make credentials difficult to rotate and risks placing secrets in
image layers.

`Dockerfile.with-user-env` should remain an immutable carrier for the provider
CLI binaries. It should create only neutral directory roots and declare stable
defaults.

### The current image assumes one account per provider

The runtime currently creates one fixed directory for each provider:

- `/home/nonroot/.gemini`
- `/home/nonroot/.codex`
- `/home/nonroot/.claude`

The entrypoint then fixes `HOME` to `/home/nonroot` and derives `CODEX_HOME` from
that location. The Compose override mounts one fixed credential volume at each
of those paths. Consequently, a new login replaces the active credentials for
that provider.

### Compose profiles are the wrong identity mechanism

The existing `profiles: ["login"]` entries correctly control whether one-shot
login services are available. Creating Compose profiles such as `work` and
`personal` would mix two unrelated concepts, duplicate service definitions, and
require a Compose edit for every new account.

Account selection should instead be runtime data passed to a generic login
service.

### Credential paths must be explicit

An ambient, process-wide `HOME` works for one account but cannot represent
several accounts being polled concurrently. Each provider agent or CLI runner
must receive the credential directory for its account explicitly.

Provider-specific mapping is still necessary:

| Provider | Account-scoped state |
|---|---|
| Codex | `CODEX_HOME`, containing `auth.json` and minimal CLI state |
| Claude Code | A dedicated `HOME`, containing `.claude/.credentials.json` |
| Antigravity | A dedicated `HOME`, including `.gemini`, `.codeium`, cache, and the keyring |

### Authentication isolation must remain provider-scoped

The existing one-shot services deliberately receive no `.env`, no `/data`, and
only their provider's credential volume. This boundary should remain.

A provider-root volume containing several accounts means that a provider's
login CLI can see sibling accounts for that same provider. This is a deliberate
tradeoff that enables accounts to be added without changing Compose. If
account-to-account filesystem isolation is required, each account needs its own
named volume and the Compose model must be generated or edited for every
account.

Provider-root volumes are the recommended default because they preserve
cross-provider isolation while keeping account management practical.

### Docker support and multi-account polling are separate milestones

The repository already has application-level multi-account support for Codex,
including `provider_accounts`, a `CodexAgentManager`, account-scoped snapshots,
and dashboard account selection. Antigravity and Anthropic do not yet have the
equivalent complete path.

For Antigravity specifically, separate homes are necessary but insufficient.
Its CLI runner also has one warm session, and its snapshot storage does not yet
fully separate accounts. The related design is documented in
`docs/plans/2026-08-08-antigravity-multi-account-design.md`.

### D-Bus addresses must not be persisted

`DBUS_SESSION_BUS_ADDRESS` identifies an ephemeral Unix socket belonging to a
specific running container session. Saving it in a named volume would leave a
stale or unreachable address after a container restart and would not allow a
separate `docker compose run` container to attach to the original socket.

Only the Antigravity account files and GNOME Keyring files should persist. Each
login container or managed Antigravity runner should start its own D-Bus and
keyring session and keep the resulting address in its process environment or
temporary runtime directory.

## Recommended Runtime Contract

### Directory layout

Use one persistent root per provider and one validated directory per account:

```text
/auth/
|-- codex/
|   |-- work/
|   `-- personal/
|-- claude/
|   |-- work/
|   `-- personal/
`-- antigravity/
    |-- work/
    `-- personal/
```

The provider roots are backed by separate named volumes:

```yaml
volumes:
  antigravity-auth:
  codex-auth:
  claude-auth:
```

The daemon mounts all three because polling requires their credentials. Each
login service mounts only its own provider root.

### Account selector

Use one variable only for the one-shot login operation:

```text
ONWATCH_LOGIN_ACCOUNT=default
```

Account names must match:

```text
[a-z0-9][a-z0-9_-]{0,31}
```

This makes names safe as directory components, stable identifiers, and default
dashboard labels. The entrypoint must reject invalid names with exit code 64
before creating directories or starting privileged services.

Example usage:

```bash
docker compose --profile login run --rm \
  -e ONWATCH_LOGIN_ACCOUNT=work codex-login

docker compose --profile login run --rm \
  -e ONWATCH_LOGIN_ACCOUNT=personal claude-login

docker compose --profile login run --rm \
  -e ONWATCH_LOGIN_ACCOUNT=work agy-login
```

The default remains `default`, preserving the current commands for users who do
not need multiple accounts.

### Entrypoint routing

The entrypoint derives provider-specific state from the selected account while
preserving the exact command allowlist:

```sh
account="${ONWATCH_LOGIN_ACCOUNT:-default}"

case "$account" in
  ""|*[!a-z0-9_-]*)
    echo "onwatch: invalid login account '$account'" >&2
    exit 64
    ;;
esac

case "${1:-}" in
  codex)
    export HOME="/home/nonroot"
    export CODEX_HOME="/auth/codex/$account"
    ;;
  claude)
    export HOME="/auth/claude/$account"
    ;;
  agy)
    export HOME="/auth/antigravity/$account"
    export XDG_RUNTIME_DIR="/tmp/onwatch-runtime/antigravity/$account"
    ;;
esac
```

The account selector is an entrypoint concern and is never forwarded as an
argument to the upstream CLIs.

For Antigravity, D-Bus and GNOME Keyring should be started after selecting the
account so they inherit the correct `HOME` and `XDG_RUNTIME_DIR`.

### Account discovery

The daemon should discover accounts by scanning the provider roots rather than
requiring a second comma-separated account list. A valid child directory is a
candidate account. The provider-specific detector confirms whether usable
credentials exist before starting an agent.

This avoids two sources of truth:

- Login creates or updates the account directory.
- The daemon discovers that directory.
- Removing a directory stops future polling and soft-deletes the corresponding
  `provider_accounts` row while preserving historical telemetry.

The directory name is a user-facing alias. The provider's stable account ID,
email, or token identity remains the canonical duplicate-detection key.

## Implementation Plan

Implementation must follow the repository's TDD-first requirement.

### Phase 1 - Define and test the container contract

1. Add failing entrypoint tests for:
   - the default account;
   - valid named accounts;
   - invalid and traversal-like names;
   - provider-specific `HOME`, `CODEX_HOME`, and `XDG_RUNTIME_DIR` values;
   - exact login-command allowlisting remaining unchanged;
   - non-CLI arguments continuing to dispatch to `/app/onwatch`.
2. Introduce `ONWATCH_LOGIN_ACCOUNT`, defaulting to `default`.
3. Add a small entrypoint function that validates the account before any
   directory creation or `chown`.
4. Keep `scripts/test-entrypoint-dispatch.sh` synchronized with every allowlist
   or dispatch change.

Acceptance criteria:

- Existing single-account login commands work without new flags.
- Invalid names exit 64 and create no filesystem state.
- The upstream CLI never receives the account selector.
- Agentic CLI commands remain refused.

### Phase 2 - Move credential volumes to provider roots

1. Change the runtime image's directory creation from fixed provider homes to:
   - `/auth/codex`
   - `/auth/claude`
   - `/auth/antigravity`
   - `/tmp/onwatch-runtime/antigravity`
2. Update the daemon and login service mounts in
   `docker-compose.override-with-user-env.yml`.
3. Ensure each login service still has no `env_file`, `/data`, published port,
   or other provider's volume.
4. Seed Codex's history-disabled `config.toml` inside the selected account's
   `CODEX_HOME`.
5. Add an explicit, idempotent migration from the legacy fixed directories to
   each provider's `default` account. Never overwrite a populated destination.
6. Document recovery and rollback before removing legacy-path support.

Acceptance criteria:

- Two logins for one provider persist in different directories.
- Rebuilding or restarting the image preserves all accounts.
- `docker compose --profile login config` shows that login services remain
  provider-isolated.
- Existing named volumes migrate to `default` without losing credentials.

### Phase 3 - Add account-root configuration to the daemon

1. Add provider-specific auth-root configuration with container defaults and
   host-install fallbacks.
2. Implement a shared account-directory scanner that:
   - ignores hidden, invalid, and non-directory entries;
   - returns stable, sorted aliases;
   - handles a missing root as no configured accounts;
   - never follows a path outside the configured root.
3. Add provider-specific credential validation and stable identity extraction.
4. Reconcile discoveries with `provider_accounts`, using soft deletion for
   removed accounts.
5. Poll for directory changes on a bounded interval, following the existing
   Codex manager precedent.

Acceptance criteria:

- Adding a valid account directory is detected without restarting onWatch.
- Duplicate aliases pointing to the same provider identity are not polled twice.
- Removing credentials stops polling but preserves historical data.
- No credential contents or token values are logged.

### Phase 4 - Adapt Codex without creating a second profile system

Codex already stores copied profile JSON under `/data/codex-profiles`. Avoid
running that system and `/auth/codex/<account>` as independent sources of truth.

1. Choose one migration direction:
   - preferred: teach `CodexAgentManager` to load native account-scoped
     `auth.json` files directly; or
   - compatibility path: import account-scoped `auth.json` into the existing
     profile format through one well-defined reconciliation step.
2. Preserve existing profile names, database account IDs, refresh-token
   write-back, and duplicate-account merging.
3. Keep the legacy `/data/codex-profiles` reader during a documented transition
   period.

Acceptance criteria:

- Existing Codex profiles retain their history and database identity.
- Native login directories and legacy profiles cannot start duplicate agents.
- OAuth token rotation is written back to the active account's credential
  source.

### Phase 5 - Add Anthropic multi-account polling

1. Parameterize Anthropic credential detection and refresh functions with an
   explicit credentials path or account home.
2. Add an `AnthropicAgentManager`, following the Codex manager's lifecycle and
   polling-toggle patterns.
3. Add `account_id` to Anthropic snapshots, cycles, and every related read path
   that is not already account-scoped.
4. Ensure refreshed access and refresh tokens are written only to the matching
   account's `.credentials.json`.
5. Extend APIs, settings, dashboard selection, and notifications with the
   account ID.

Acceptance criteria:

- Two Claude accounts poll independently.
- A refresh-token rotation for one account cannot alter the other account.
- Latest, history, cycle, and notification queries cannot mix accounts.

### Phase 6 - Add Antigravity multi-account polling

Coordinate this phase with
`docs/plans/2026-08-08-antigravity-multi-account-design.md`.

1. Add explicit per-account `HOME`, `XDG_RUNTIME_DIR`, and D-Bus environment
   options to `AntigravityCLIRunner`.
2. Create a fresh D-Bus/keyring session for each active runner or login
   invocation; do not persist its bus address.
3. Add an `AntigravityAgentManager` with one runner per discovered account.
4. Serialize Antigravity polling and disable warm sessions when more than one
   account is configured until measured memory data proves concurrency safe.
5. Add `account_id` to Antigravity storage and scope every latest, history,
   model-value, and cycle query.
6. Add account-level duplicate detection using the provider identity returned by
   the CLI.

Acceptance criteria:

- Each runner uses only its account home and keyring.
- No persisted D-Bus address is required after restart.
- Two Antigravity polls never overlap under the serialized policy.
- Memory stays inside the documented container ceiling.
- Telemetry from separate Google accounts never interleaves.

### Phase 7 - Generalize the dashboard and HTTP API

1. Extract the Codex-specific account selector into reusable provider-account UI
   components.
2. Support `account_id` consistently for current, history, cycles, summary, and
   settings endpoints.
3. Add an aggregate `All accounts` view while retaining clear account labels.
4. Show deleted accounts only where historical-data controls require them.
5. Ensure notifications include provider and account labels.

Acceptance criteria:

- Account switching behaves consistently for Codex, Anthropic, and
  Antigravity.
- Aggregate views never imply that quotas from different subscriptions form one
  shared allowance.
- Deleted accounts cannot be re-enabled for polling without credentials.

### Phase 8 - Documentation, migration, and verification

1. Update `docs/WITH_USER_ENV.md`, provider setup guides, Compose examples, and
   the volume reference.
2. Document the provider-root isolation tradeoff and the stricter per-account
   volume alternative.
3. Add multi-account login, status, logout, migration, backup, and deletion
   examples.
4. Verify the merged Compose configuration.
5. Run the repository-required checks through `app.sh`:

```bash
./app.sh --smoke
./app.sh --test
```

6. Re-measure Antigravity memory with multiple configured accounts and update the
   measured resource budget.

Acceptance criteria:

- Container tests cover both supported architectures where practical.
- Documentation describes the actual trust boundary and persisted files.
- Single-account users require no configuration changes after migration.
- All account-scoped store tests pass under race detection.

## Recommended Delivery Order

Ship the work in independently reviewable changes:

1. Entrypoint account selection and validation tests.
2. Provider-root volumes and legacy migration.
3. Shared account discovery and database reconciliation.
4. Codex source-of-truth migration.
5. Anthropic manager and account-scoped persistence.
6. Antigravity manager, runtime sessions, and account-scoped persistence.
7. Shared dashboard and API account selection.
8. Documentation, resource measurement, and removal of expired compatibility
   paths.

Do not combine the volume migration and database schema changes into one large
release. Keeping them separate makes rollback possible and allows the container
contract to be verified before account-scoped telemetry depends on it.

## Decisions Required Before Implementation

1. Is provider-level isolation sufficient, or must every account have a separate
   Docker volume?
2. Should native Codex `auth.json` directories replace `/data/codex-profiles`,
   or should they be imported into the existing format?
3. Should host installations adopt the same `/auth/<provider>/<account>` model,
   or should initial delivery be container-only?
4. How long should legacy fixed credential paths remain supported after
   migration?

