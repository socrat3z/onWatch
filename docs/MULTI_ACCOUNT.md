# Multiple Accounts Per Provider

Track two or more accounts of the same provider side by side - a work Claude
login and a personal one, two Antigravity profiles, several Codex logins - each
with its own quota history, its own reset cycles, and its own credentials.

Supported for **Anthropic**, **Antigravity**, and **Codex**. Each one discovers
accounts from a directory you control: one folder per account, holding exactly
the credential file that provider's CLI already writes.

> Running the fork's container image? The login commands that create these
> folders for you live in [with-user-env Image](WITH_USER_ENV.md#multiple-accounts).
> This guide covers the model itself and the host install.

## Contents

- [How it works](#how-it-works)
- [Setup](#setup)
  - [Anthropic](#anthropic)
  - [Antigravity](#antigravity)
  - [Codex](#codex)
- [Account names and display aliases](#account-names-and-display-aliases)
- [Using accounts in the dashboard](#using-accounts-in-the-dashboard)
- [API](#api)
- [Metrics](#metrics)
- [Account health](#account-health)
- [Adding and removing accounts](#adding-and-removing-accounts)
- [History recorded before you enabled discovery](#history-recorded-before-you-enabled-discovery)
- [Settings that are ignored in multi-account mode](#settings-that-are-ignored-in-multi-account-mode)
- [Limits and known trade-offs](#limits-and-known-trade-offs)
- [Troubleshooting](#troubleshooting)
- [What this guide does not cover](#what-this-guide-does-not-cover)

## How it works

Point one environment variable at a root directory. Every direct child directory
of that root is one account:

```
$ANTHROPIC_AUTH_ROOT/
├── work/
│   └── .claude/.credentials.json
└── personal/
    └── .claude/.credentials.json
```

Once a minute onWatch rescans the root and reconciles what it finds:

1. **Discovery** lists the child directories. It never follows symlinks, and it
   skips any child whose resolved path escapes the root, so a symlinked folder
   cannot make onWatch read credentials from elsewhere on the filesystem.
2. **Registration** creates (or restores) one `provider_accounts` row per folder.
   The folder name is the account's durable identity; it is never renamed.
3. **Polling** starts one agent per account, each with its own HTTP client,
   tracker, session manager, and credential file. An OAuth refresh performed for
   one account writes only that account's file.
4. **Retirement** soft-deletes an account whose folder has disappeared. Polling
   stops, history is kept.

Discovery reads only *whether* the credential file exists and parses. It never
reads a secret to decide membership, and it never writes to a credential folder
except to persist a token rotation for that same account.

## Setup

Set the variable in `.env` (or the environment) and restart the daemon. Creating
the folders and logging in is a manual, one-time step per account, done with the
provider's own CLI.

### Anthropic

```bash
ANTHROPIC_AUTH_ROOT=/home/you/.onwatch/auth/claude
```

Expected layout - the standard Claude Code home, one per account:

```
<root>/<account>/.claude/.credentials.json
```

To create one, run Claude Code's login with `HOME` pointed at the account
folder, so it writes its credentials there instead of your real home:

```bash
mkdir -p "$ANTHROPIC_AUTH_ROOT/work"
HOME="$ANTHROPIC_AUTH_ROOT/work" claude
```

Each account rotates its own refresh token. Refresh tokens are one-time use, so
this isolation is what keeps one account's rotation from invalidating another's.

### Antigravity

```bash
ANTIGRAVITY_AUTH_ROOT=/home/you/.onwatch/auth/antigravity
```

Expected layout - the account folder is used directly as `HOME` for the `agy`
CLI, which keeps its profile in `.gemini` below it:

```
<root>/<account>/.gemini/
```

```bash
mkdir -p "$ANTIGRAVITY_AUTH_ROOT/work"
HOME="$ANTIGRAVITY_AUTH_ROOT/work" agy
```

Setting this variable enables the Antigravity provider on its own, and every
named account polls through the `agy` CLI (see
[Settings that are ignored](#settings-that-are-ignored-in-multi-account-mode)).
The `agy` binary must be on the daemon's `PATH`.

### Codex

```bash
CODEX_AUTH_ROOT=/home/you/.onwatch/auth/codex
```

Expected layout - a native `CODEX_HOME`, one per account:

```
<root>/<account>/auth.json
```

```bash
mkdir -p "$CODEX_AUTH_ROOT/work"
CODEX_HOME="$CODEX_AUTH_ROOT/work" codex login
```

Codex additionally keeps its older saved-profile mechanism (see
[Codex Setup](CODEX_SETUP.md)); the two coexist. Native accounts load after
legacy profiles, so if a profile and a folder share a name the legacy profile
wins and the native account never polls. onWatch logs one `WARN` naming the
alias and the winner - rename one of the two.

## Account names and display aliases

The **folder name** is the account name. It must match
`^[a-z0-9][a-z0-9_-]{0,31}$`: start with a lowercase letter or digit, then
lowercase letters, digits, `_`, or `-`, up to 32 characters. Anything else is
skipped by discovery - it is not an error, the folder is simply not an account.
The rule is strict because this name is also used as a path segment.

The **alias** is a display label of 1-64 characters, stored as account metadata
and editable from the dashboard or the API. Editing an alias never moves or
renames a credential folder, so discovery keeps working across a rename and
history stays attached to the same account.

## Using accounts in the dashboard

Anthropic and Antigravity views show an account picker as soon as one account is
discovered - it appears even at a single account, so a first login is visibly
confirmed rather than silent. Switching accounts re-reads every panel for that
account: current quotas, history, cycles, sessions, and insights.

The picker also offers **Edit display alias**, and marks any account that cannot
poll with the reason and the exact path onWatch checked.

The homepage shows one widget per account inside the provider's card: every live
account, its own quotas, and its own freshness chip, so a stale profile cannot
hide behind a healthy sibling. Clicking a profile opens that provider's view
already switched to that account.

Per-quota provider views other than the homepage stay on one account at a time -
the account picker chooses which.

## API

`GET /api/accounts?provider=anthropic|antigravity` lists account IDs, folder
names, display aliases, deletion state, and credential health.

```http
PATCH /api/accounts?provider=anthropic
Content-Type: application/json

{"account_id": 12, "alias": "Acme work"}
```

Read endpoints take `&account=<id>`: `/api/current`, `/api/history`,
`/api/cycles`, `/api/cycle-overview`, `/api/sessions`, `/api/logging-history`.
Omitting it selects the provider's default account. An ID that belongs to a
different provider is rejected rather than silently answered from the wrong
data. Full response shapes are in [HTTP API](HTTP_API.md).

## Metrics

Every quota, credit, and agent-health series carries a numeric `account_id`.
Join it to a readable name with `onwatch_account_info`:

```promql
onwatch_quota_utilization_percent
  * on(account_id) group_left(account_name) onwatch_account_info
```

Only live accounts are exported. A soft-deleted account would otherwise pin a
frozen value and grow label cardinality forever; its history stays queryable
through the dashboard and API. See [Prometheus Metrics](PROMETHEUS_METRICS.md).

## Account health

Each reconcile records the state of the credential file the provider needs:

| State | Meaning |
|-------|---------|
| `ok` | Found and readable. The account polls. |
| `missing` | The expected file is not there. Registered, not polling - finish the login. |
| `unreadable` | Present but malformed or empty. Registered, not polling - log in again. |
| `unverified` | Discovery cannot tell (Antigravity keeps its token in the keyring, which discovery deliberately never reads). Polling proceeds; the `agy` CLI reports the real state. |

The state and the path checked are returned by `GET /api/accounts` and shown in
the picker, so an account with no data explains itself instead of rendering an
empty dashboard.

## Adding and removing accounts

**Adding**: create the folder and log in. The next reconcile (within a minute)
registers it and starts polling. No restart.

**Removing**: delete the folder. The next reconcile stops that account's agent
and soft-deletes the row. History is retained and remains visible as a deleted
account; Prometheus stops exporting its series. This works for an account that
never successfully polled, too.

If the whole root is unreadable - an unmounted volume, a typo in the path -
reconciliation stands down instead of retiring every account at once, because
"the mount is gone" and "the user deleted everything" look identical from here.

## History recorded before you enabled discovery

Everything polled before discovery was on stays attached to a reserved account
named `default`, and nothing is reassigned automatically - a merge would have to
rewrite reset cycles, and that is not reversible.

- The picker shows `default` labelled **Before account split**.
- If a `default` credential folder exists, it also keeps polling as a live
  account and the history is continuous.
- If not, `default` stops gaining data but keeps all of it. Reconciliation never
  soft-deletes `default`, because unlike every other account it is not backed by
  a credential directory.

For the container image, existing single-account volumes are copied to `default`
on first start, with the originals kept as a recovery copy. The SQLite migration
is automatic in every install - no manual database step.

## Settings that are ignored in multi-account mode

Both emit one startup `WARN` naming the setting and the reason:

| Setting | Ignored when | Why |
|---------|--------------|-----|
| `ANTHROPIC_TOKEN` | `ANTHROPIC_AUTH_ROOT` is set | Every named account authenticates from its own `<root>/<alias>/.claude/.credentials.json` and rotates its own refresh token. |
| `ANTIGRAVITY_SOURCE` | `ANTIGRAVITY_AUTH_ROOT` is set | The IDE probe cannot be scoped to one account home, so every named account polls through the `agy` CLI. |

## Limits and known trade-offs

- **Antigravity memory**: onWatch keeps at most **one** warm `agy` process
  (~190 MiB resident) regardless of account count, because two would not fit the
  512M container budget. With two or more Antigravity accounts they trade that
  slot, so each pays an `agy` cold start (~15s) per poll instead of reusing a
  warm process. Polls are serialized, never concurrent.
- **Combined views**: the homepage lists every account, but its widgets are
  current quotas only - history charts and insights stay on the provider view,
  one account at a time.
- **No merging**: pre-discovery `default` history cannot be merged into a named
  account.
- **Aliases are labels**: they never affect discovery, file paths, or which
  credentials are used.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| An account never appears | Folder name fails the naming rule | Rename to lowercase letters, digits, `_`, `-`, starting alphanumeric, ≤32 chars |
| Account listed, no data, health `missing` | Login never completed in that folder | Re-run the provider login with `HOME`/`CODEX_HOME` pointed at the account folder |
| Account listed, no data, health `unreadable` | Credential file truncated or malformed | Log in again for that account |
| Dashboard shows nothing while accounts poll fine | Looking at a combined view or the `default` account | Use the account picker on the provider's own page |
| A Codex folder never polls | Alias collides with a legacy Codex profile | Check the startup `WARN`; rename one of the two |
| `account scan failed` every minute | The root path is missing or unreadable | Fix the path, or unset the variable to disable discovery |
| Symlinked account folder ignored | Discovery does not follow symlinks, by design | Use a real directory, or bind-mount it |

## What this guide does not cover

- **MiniMax** accounts, which are API keys managed in the dashboard rather than
  discovered from disk - see [MiniMax Setup](MINIMAX_SETUP.md).
- **Codex legacy profiles**, the older saved-credential mechanism that coexists
  with `CODEX_AUTH_ROOT` - see [Codex Setup](CODEX_SETUP.md).
- **Container logins**, the `*-login` compose services that create these folders
  for you - see [with-user-env Image](WITH_USER_ENV.md#multiple-accounts).
