# with-user-env Image

The optional container image that runs provider CLIs inside the container, for the
providers whose quota data needs an interactive OAuth login rather than a static
API key: **Antigravity** (`agy`), **Codex** (`codex`), and **Anthropic** (`claude`).

This is a fork-only addition. The stock `Dockerfile` and `docker-compose.yml` are
untouched, so the default build is still upstream's distroless image.

## Multiple accounts

Each provider uses a root with one safe, stable credential folder per account.
Choose a lowercase local name such as `work` or `personal`, then use it only for
the one-shot login command:

```bash
docker compose --profile login run --rm -e ONWATCH_LOGIN_ACCOUNT=work codex-login
docker compose --profile login run --rm -e ONWATCH_LOGIN_ACCOUNT=personal claude-login
docker compose --profile login run --rm -e ONWATCH_LOGIN_ACCOUNT=work agy-login
```

Omit `ONWATCH_LOGIN_ACCOUNT` for `default`. Names allow lowercase letters,
numbers, `_`, and `-`, up to 32 characters. The dashboard discovers accounts,
offers a clear account picker, and lets users save a human-friendly display
alias without renaming the credential folder. Removing a folder stops polling
but retains its historical usage as a deleted account.

Existing single-account volumes are copied to `default` on first start and the
original files are retained as a recovery copy. SQLite data is migrated
automatically; no manual database step is required.

> **Everything about this image lives in this document.** The provider setup guides
> ([Antigravity](ANTIGRAVITY_SETUP.md), [Codex](CODEX_SETUP.md)) cover host installs
> and provider configuration and link back here for the container path.

---

## Contents

- [Why this image exists](#why-this-image-exists)
- [Files](#files)
- [Quick start](#quick-start)
- [Authenticating](#authenticating)
  - [Codex](#codex)
  - [Anthropic](#anthropic)
  - [Antigravity](#antigravity)
  - [Checking and clearing a login](#checking-and-clearing-a-login)
- [The trust boundary](#the-trust-boundary)
  - [Daemon container vs login containers](#daemon-container-vs-login-containers)
  - [The command allowlist](#the-command-allowlist)
  - [Build-time supply chain](#build-time-supply-chain)
  - [Privilege drop](#privilege-drop)
- [Volume reference](#volume-reference)
- [Updating the pinned CLIs](#updating-the-pinned-clis)
- [Testing](#testing)
- [Measured resource budget](#measured-resource-budget)
- [Troubleshooting](#troubleshooting)
- [Known limitations](#known-limitations)

---

## Why this image exists

Most onWatch providers authenticate with an API key from `.env`. Three do not:
their quota endpoints require an OAuth token that only the vendor's own CLI can
mint, through an interactive browser flow.

The default distroless image has no shell and no CLIs, so on a headless host there
is no way to produce those tokens without installing the vendor CLIs on the host
and copying credential files into the container. This image removes that step: it
bundles the three CLIs purely so the login can happen **inside** the container,
writing tokens to a named volume that onWatch then polls.

Polling itself reads token files, not CLIs - with one exception. With
`ANTIGRAVITY_SOURCE=cli`, the daemon spawns `agy` as a subprocess and keeps it warm
for five minutes (`agyDefaultWarmTTL`, `internal/api/antigravity_cli.go`). That
exception drives the memory budget below.

---

## Files

| File | Purpose |
|------|---------|
| `Dockerfile.with-user-env` | Debian-based runtime with the `agy`, `codex`, and `claude` CLIs, D-Bus, and gnome-keyring |
| `docker-compose.override-with-user-env.yml` | One volume per credential store, plus three isolated one-shot login services |
| `docker-entrypoint-with-user-env.sh` | Allowlists the login commands, starts D-Bus and the keyring, then drops to the `nonroot` user |
| `scripts/test-entrypoint-dispatch.sh` | Allowlist contract tests (no Docker required) |
| `scripts/test-with-user-env-image.sh` | Image smoke checks: staged binaries, pruning, UID drop, volume writability |
| `.github/workflows/ci.yml` (`with-user-env-image` job) | Runs both scripts and builds `linux/amd64` + `linux/arm64` |

The image has three build stages:

| Stage | Base | Role |
|-------|------|------|
| `builder` | `golang:1.25-alpine` | Compiles the static onWatch binary (shared with the stock Dockerfile) |
| `cli-installer` | `debian:bookworm-slim` | Throwaway. Downloads and checksums the pinned CLI archives, stages one native binary each |
| `runtime` | `debian:bookworm-slim` | Final image. Debian rather than distroless because the vendor CLIs are glibc binaries needing a Secret Service |

---

## Quick start

Layer both Compose files explicitly - Compose only auto-loads the exact name
`docker-compose.override.yml`, which this deliberately is not:

```bash
docker compose -f docker-compose.yml -f docker-compose.override-with-user-env.yml up -d
```

Or set it once in your `.env` so plain `docker compose` commands pick it up:

```
COMPOSE_FILE=docker-compose.yml:docker-compose.override-with-user-env.yml
```

Without either, plain `docker compose up -d` builds the stock upstream image and
every login command below fails with `no such service: codex-login`.

---

## Authenticating

Each provider logs in through its own one-shot service. Build the image once, then
run only the logins you need. Each writes its tokens to a dedicated named volume
and exits; onWatch polls the token files from then on.

```bash
docker compose build onwatch
docker compose --profile login run --rm agy-login     # Antigravity
docker compose --profile login run --rm codex-login   # Codex
docker compose --profile login run --rm claude-login  # Anthropic
```

The three flows are **not** the same. None of them needs a browser in the
container, a published port, or a callback reaching the container.

### Codex

Runs `codex login --device-auth`, the OAuth device flow. The CLI prints a URL and
a short one-time code. Approve the code at `chatgpt.com` from any device with a
browser; the CLI receives its tokens from the server. **Nothing is typed back into
the container.**

Tokens land in `auth.json` inside the `codex-auth` volume. onWatch reads that file
via `CODEX_HOME=/home/nonroot/.codex`.

For a second account, log in again and save it as a profile - see
[Multi-Account Support](CODEX_SETUP.md#multi-account-support-v21112). Profiles live
in `/data/codex-profiles/`, not in the auth volume.

### Anthropic

Runs `claude auth login`. The CLI prints an authorization URL. After you sign in,
the browser cannot reach the CLI's local callback server from inside a container,
so it displays a code instead. **Paste that code back into the container terminal**
at the prompt.

Tokens land in `.credentials.json` inside the `claude-auth` volume.

If you would rather not run an interactive flow in the container at all, Anthropic
also supports a long-lived token: run `claude setup-token` on a machine with a
browser, then set the printed value as `CLAUDE_CODE_OAUTH_TOKEN`. That path needs
no volume and no login container.

### Antigravity

Runs bare `agy`. Antigravity has **no login subcommand** - it authenticates on
first run. Copy the printed authorization URL into a browser, sign in, paste the
returned code back into the terminal, then exit the CLI.

Its OAuth session lives in the Linux Secret Service rather than a file, which is
why this image carries D-Bus and gnome-keyring and why the service mounts both
`antigravity-profile` and `antigravity-keyring`.

Enable the CLI source in your `.env` before starting the daemon:

```bash
ANTIGRAVITY_ENABLED=true
ANTIGRAVITY_SOURCE=cli
```

To log out, use the in-TUI `/logout` command, or delete the two volumes.

### Checking and clearing a login

Override the service command with any other allowlisted one:

```bash
docker compose --profile login run --rm codex-login  codex login status
docker compose --profile login run --rm codex-login  codex logout
docker compose --profile login run --rm claude-login claude auth status
docker compose --profile login run --rm claude-login claude auth logout
```

---

## The trust boundary

Be precise about this. The two container roles differ, and conflating them
overstates the isolation.

### Daemon container vs login containers

| | `onwatch` daemon | `*-login` containers |
|---|---|---|
| `./onwatch-data:/data` host bind mount | **yes** (inherited from `docker-compose.yml`) | no |
| `env_file: .env` - every provider secret | **yes** | no |
| Published port 9211 | **yes** | no |
| Credential volumes | all four | only its own provider's |

The login services are declared **from scratch** in the override rather than
derived from the `onwatch` service, which is what keeps them clean. A Codex login
cannot read your Anthropic tokens, your `.env`, or `onwatch.db`.

Verify this yourself after any Compose edit - do not take it on faith:

```bash
docker compose -f docker-compose.yml -f docker-compose.override-with-user-env.yml \
  --profile login config
```

Confirm that `/data`, `ports:`, and your `.env` keys appear only under the
`onwatch` service.

### The command allowlist

The entrypoint accepts these and nothing else:

```
agy
codex login --device-auth | codex login status | codex logout
claude auth login | claude auth status | claude auth logout | claude setup-token
```

Every other invocation exits **64** without running - `codex exec`, bare `claude`,
`claude -p`, `claude mcp list`, `agy install`, `agy plugin list`, and so on. These
are full agentic CLIs; unrestricted, one invoked in the daemon container would
inherit every value in `.env` and the writable `/data` mount.

The check runs before any privileged work, which is what makes it testable without
Docker. `scripts/test-entrypoint-dispatch.sh` pins the contract in both directions:
allowlisted commands dispatch, agentic ones are rejected, and non-CLI arguments
still reach the daemon.

Changing the allowlist means changing that test in the same commit.

### Build-time supply chain

Codex and Claude Code are installed as **verified binaries, not packages**:

- Fetched by exact version from the npm registry as release tarballs.
- Each archive SHA-256 checked against a pin in the Dockerfile before unpacking.
- Reduced to its single native binary in the throwaway `cli-installer` stage.
- `TARGETARCH` maps to an exact upstream path - no `find | head -1` heuristics, and
  an unsupported architecture fails the build with a clear message.

No package manager, install script, or dependency lifecycle hook runs at build
time. No Bun, Node, npm, or `node_modules` reaches the final image. Nothing in the
image can install or update a package.

Upstream layout, pinned and guarded by `test -x` assertions:

| Package | Path to binary |
|---|---|
| `@openai/codex-linux-x64` | `vendor/x86_64-unknown-linux-musl/bin/codex` |
| `@openai/codex-linux-arm64` | `vendor/aarch64-unknown-linux-musl/bin/codex` |
| `@anthropic-ai/claude-code-linux-x64` | `claude` |
| `@anthropic-ai/claude-code-linux-arm64` | `claude` |

Codex's 48 MB `codex-code-mode-host` is pruned; a login never uses it.

### Privilege drop

Root runs briefly at container start to fix ownership of freshly created named
volumes (Docker creates them root-owned), then `gosu` drops to UID 65532 for the
actual process. `exec` is used throughout, so PID 1 ends up being the real process
and signals propagate correctly.

The CLIs themselves live in `/opt/provider-cli`, root-owned and outside every
writable volume.

Chatty behaviour is off by default: Codex session history via a seeded
`~/.codex/config.toml`, and Claude Code auto-update, telemetry, and non-essential
traffic via environment variables.

---

## Volume reference

One volume per credential store. No source tree, project directory, or host path is
mounted into a login container. Deleting one volume logs out that provider only.

| Volume | Mounted at | Written by | Contents |
|---|---|---|---|
| `antigravity-profile` | `~/.gemini` | `agy` | CLI settings and profile |
| `antigravity-keyring` | `~/.local/share/keyrings` | gnome-keyring | Encrypted OAuth session (Secret Service) |
| `codex-auth` | `~/.codex` | `codex` + entrypoint | See below |
| `claude-auth` | `~/.claude` | `claude` | `.credentials.json` |

### What `codex-auth` actually holds

This is a dedicated Codex **state** directory, not a credentials-only store:

| Path | Written by | Purpose |
|------|-----------|---------|
| `auth.json` | `codex login` | The OAuth tokens onWatch reads |
| `config.toml` | the entrypoint, once | Seeds `history.persistence = "none"` so no session transcripts accumulate |
| other files | the Codex CLI | Anything else the pinned CLI version chooses to write, such as version or cache state |

The seeded config is what keeps the volume small and transcript-free. It is not a
guarantee that `auth.json` is the only file present.

---

## Updating the pinned CLIs

Codex and Claude Code are pinned by version **and** SHA-256 in
`Dockerfile.with-user-env`. Change them together, in one reviewable commit.

1. Pick the new version and record both architectures' checksums:

   ```bash
   CODEX_VERSION=0.147.0
   CLAUDE_VERSION=2.1.226
   for u in \
     "https://registry.npmjs.org/@openai/codex/-/codex-${CODEX_VERSION}-linux-x64.tgz" \
     "https://registry.npmjs.org/@openai/codex/-/codex-${CODEX_VERSION}-linux-arm64.tgz" \
     "https://registry.npmjs.org/@anthropic-ai/claude-code-linux-x64/-/claude-code-linux-x64-${CLAUDE_VERSION}.tgz" \
     "https://registry.npmjs.org/@anthropic-ai/claude-code-linux-arm64/-/claude-code-linux-arm64-${CLAUDE_VERSION}.tgz" ; do
     echo "$(curl -sL "$u" | sha256sum | cut -d' ' -f1)  $u"
   done
   ```

2. Update `CODEX_VERSION`, `CLAUDE_VERSION`, and all four `*_SHA256_*` build args.
3. Build both architectures: `./scripts/test-with-user-env-image.sh --all-arch`.
4. Read the upstream release notes for changes to authentication, subcommand names,
   or credential paths. **If a login command changed, update the allowlist in
   `docker-entrypoint-with-user-env.sh` and its tests in the same commit.**
5. Re-measure image size and memory, and update
   [Measured resource budget](#measured-resource-budget) with the new date and
   versions.

Finding the current versions:

```bash
curl -s https://registry.npmjs.org/@openai/codex/latest        | grep -o '"version":"[^"]*"'
curl -s https://registry.npmjs.org/@anthropic-ai/claude-code/latest | grep -o '"version":"[^"]*"'
```

---

## Testing

Two scripts, kept out of the Go suite because they validate Docker packaging,
process ownership, and Compose inheritance rather than Go behaviour.

```bash
./scripts/test-entrypoint-dispatch.sh          # no Docker needed, ~1s
./scripts/test-with-user-env-image.sh          # builds and exercises the image
./scripts/test-with-user-env-image.sh --all-arch   # + cross build for the other arch
```

`test-entrypoint-dispatch.sh` covers 24 cases: every allowlisted login command
dispatches, twelve agentic or malformed invocations are rejected with exit 64, and
non-CLI arguments still route to the daemon (including that `codexprofile` is not
treated as a `codex` prefix match).

`test-with-user-env-image.sh` covers: staged binaries report the pinned versions,
Bun/Node/npm/`codex-code-mode-host` are absent, PID 1 runs as UID 65532, allowlisted
status commands dispatch while agentic ones are refused, and all four credential
directories are writable by UID 65532. The `--all-arch` cross build is build-only -
it proves the `TARGETARCH` mapping and checksums resolve on both architectures
without needing qemu.

Both run in CI on every push and pull request, alongside a `linux/amd64` +
`linux/arm64` build.

---

## Measured resource budget

Measured **2026-08-10**, `linux/amd64`, Docker Desktop, with codex 0.147.0,
claude-code 2.1.226, agy 1.1.11:

| Measurement | Value |
|---|---|
| Image size (`runtime` target) | 342 MB |
| Daemon container, idle (onWatch + D-Bus + gnome-keyring) | 9.4 MiB |
| `onwatch` process RSS | 19.8 MB |
| `gnome-keyring-daemon` RSS | 8.2 MB |
| `dbus-daemon` RSS | 2.4 MB |
| `agy` binary on disk | 190 MB |
| Peak RSS of one `agy --version` | 134 MB |

The daemon limit is **512M** rather than something near upstream's 64M for one
reason: with `ANTIGRAVITY_SOURCE=cli` the daemon itself spawns `agy` and keeps the
process warm for five minutes. A warm session costs more than the 134 MB one-shot
figure above, so the ceiling has to accommodate it.

**If you do not use `ANTIGRAVITY_SOURCE=cli`, no `agy` process ever starts in the
daemon and you can drop that limit to 128M.**

The 512M on each `*-login` service is a ceiling for the interactive TUIs and has
**not** been profiled - those flows need a terminal, so they were not measured.
Treat it as a ceiling, not a requirement.

Reproduce any of this:

```bash
docker image inspect onwatch:latest --format '{{.Size}}'
docker stats --no-stream onwatch                # daemon steady state
docker stats --no-stream                        # during an active login
```

Re-measure after bumping a pinned CLI, and record the date and versions with the
numbers: upstream native binaries change materially between releases.

---

## Troubleshooting

**`no such service: codex-login`** - Compose is not loading the override. Set
`COMPOSE_FILE` in `.env` or pass both `-f` flags. See [Quick start](#quick-start).

**`onwatch: refusing to run '<command>'` (exit 64)** - the command is not on the
allowlist. That is intentional; see [The command allowlist](#the-command-allowlist).
Use one of the listed commands, or the matching login service.

**`executable file not found: codex`** - you are on the stock distroless image, not
this one. Same fix as the first entry.

**`couldn't access control socket: .../keyring/control`** - harmless. gnome-keyring
logs this while probing for an existing daemon before starting its own.

**Login succeeds but onWatch shows no data** - confirm the daemon and the login
service point at the same volume, and that the provider is enabled in `.env`
(`ANTIGRAVITY_ENABLED`, `ANTIGRAVITY_SOURCE=cli`, etc.). Check with
`docker compose --profile login run --rm codex-login codex login status`.

**Credential files unwritable** - the entrypoint chowns the credential dirs to
65532 at start, which requires the container to start as root. If you override
`user:` in Compose, that step is skipped.

**Debugging the daemon** - unlike the stock distroless image, this one has a shell:
`docker compose exec onwatch bash`.

---

## Known limitations

- **The Antigravity CLI cannot be pinned.** Its bootstrapper publishes no versioned
  artifact and no checksum, so `agy` always installs the current build at image
  build time. `AGY_CLI_DISABLE_AUTO_UPDATE=true` at least keeps it fixed for the
  life of an image. Codex and Claude Code are fully pinned.
- **The Debian base image is not pinned by digest.** Pinning it would freeze
  security updates unless the repository adopts a regular refresh process.
- **Login TUI memory is unmeasured.** See
  [Measured resource budget](#measured-resource-budget).
- **arm64 binaries are checksum-verified but never executed in CI.** The
  cross-architecture job is build-only; running them would need qemu.
- **The image is large** (342 MB) and carries three network-capable agent CLIs plus
  Debian, D-Bus, gnome-keyring, and `libsecret-tools`. That is the cost of
  in-container OAuth. If you can log in on a host and mount credentials, the stock
  distroless image remains the smaller and smaller-attack-surface option.
