#!/bin/sh
set -eu

# The bundled provider CLIs exist for exactly one job: the interactive OAuth
# login that writes the token files onWatch polls. They are full agentic CLIs,
# so anything beyond that job is refused here - an unrestricted `codex exec` or
# `claude -p` in the daemon container would inherit every value in .env and the
# writable /data mount.
#
# Isolation has two layers, and both matter:
#   1. This allowlist, which bounds what the CLIs can be asked to do.
#   2. The one-shot *-login services in
#      docker-compose.override-with-user-env.yml, which run these commands
#      without .env and without /data. Prefer those over `run --rm onwatch`.
#
# Antigravity has no login subcommand: `agy` authenticates on first run and
# logs out through the in-TUI /logout command, so bare `agy` is allowlisted.
#
# Validate the account before any privileged work or dry-run dispatch so a bad
# alias can never create filesystem state. This account becomes $HOME for the
# login CLI below, so it is checked against the exact same character set as
# account.ValidateName (internal/account/account.go) - keep both in sync.
#
# A single glob cannot do this safely: `[a-z0-9][a-z0-9_-]*` only constrains
# its first two characters - the trailing `*` matches any string, slashes and
# dots included, once shell globbing (not regex) is in play. Path traversal
# like `wo/../../etc` passes that check and becomes $HOME. Splitting into an
# explicit reject-anything-outside-the-set pass plus a first-character/length
# check closes that.
# Single-dash expansion: only an *unset* ONWATCH_LOGIN_ACCOUNT falls back to
# "default". An explicitly empty value is treated as invalid input below
# rather than silently becoming "default".
account="${ONWATCH_LOGIN_ACCOUNT-default}"
case "$account" in
  '')
    echo "onwatch: invalid login account '$account'" >&2
    exit 64
    ;;
esac
if [ "${#account}" -gt 32 ]; then
  echo "onwatch: invalid login account '$account'" >&2
  exit 64
fi
case "$account" in
  *[!a-z0-9_-]*)
    echo "onwatch: invalid login account '$account'" >&2
    exit 64
    ;;
esac
case "$account" in
  [a-z0-9]*) ;;
  *)
    echo "onwatch: invalid login account '$account'" >&2
    exit 64
    ;;
esac

# Checked before any privileged work so scripts/test-entrypoint-dispatch.sh can
# exercise it as an ordinary user with ONWATCH_ENTRYPOINT_DRY_RUN=1.
case "${1:-}" in
  agy|codex|claude)
    case "$*" in
      "agy") ;;
      "codex login --device-auth"|"codex login status"|"codex logout") ;;
      "claude auth login"|"claude auth status"|"claude auth logout"|"claude setup-token") ;;
      *)
        echo "onwatch: refusing to run '$*'" >&2
        echo "onwatch: the bundled provider CLIs accept only these commands:" >&2
        echo "  agy" >&2
        echo "  codex login --device-auth | codex login status | codex logout" >&2
        echo "  claude auth login | claude auth status | claude auth logout | claude setup-token" >&2
        exit 64
        ;;
    esac
    if [ "${ONWATCH_ENTRYPOINT_DRY_RUN:-}" = "1" ]; then
      echo "cli: $*"
      exit 0
    fi
    ;;
  *)
    if [ "${ONWATCH_ENTRYPOINT_DRY_RUN:-}" = "1" ]; then
      echo "onwatch: $*"
      exit 0
    fi
    ;;
esac

case "${1:-}" in
  codex) target_home=/home/nonroot; target_codex_home="/auth/codex/$account" ;;
  claude) target_home="/auth/claude/$account"; target_codex_home= ;;
  agy) target_home="/auth/antigravity/$account"; target_codex_home= ;;
  *) target_home=/home/nonroot; target_codex_home="/auth/codex/default" ;;
esac

# Named Docker volumes are initially root-owned. The Antigravity CLI writes its
# profile, Codex writes auth.json, Claude Code writes .credentials.json, and
# GNOME Keyring writes encrypted session state - all below HOME.
mkdir -p /data /auth/codex /auth/claude /auth/antigravity /tmp/onwatch-runtime/antigravity
# Idempotent, non-overwriting migration from the former one-account volume
# layouts. Old files remain available as a recovery copy until a later release.
if [ -f /auth/codex/auth.json ] && [ ! -e /auth/codex/default/auth.json ]; then
  mkdir -p /auth/codex/default
  cp -a /auth/codex/auth.json /auth/codex/default/
  if [ -f /auth/codex/config.toml ]; then cp -a /auth/codex/config.toml /auth/codex/default/; fi
fi
if [ -f /auth/claude/.credentials.json ] && [ ! -e /auth/claude/default/.claude/.credentials.json ]; then mkdir -p /auth/claude/default/.claude && cp -a /auth/claude/.credentials.json /auth/claude/default/.claude/; fi
if [ -d /legacy/antigravity-profile ] && [ ! -d /auth/antigravity/default/.gemini ]; then mkdir -p /auth/antigravity/default && cp -a /legacy/antigravity-profile /auth/antigravity/default/.gemini; fi
if [ -d /legacy/antigravity-keyring ] && [ ! -d /auth/antigravity/default/.local/share/keyrings ]; then mkdir -p /auth/antigravity/default/.local/share && cp -a /legacy/antigravity-keyring /auth/antigravity/default/.local/share/keyrings; fi
# The login CLI gets $HOME=$target_home. Only the store roots exist at this
# point, so the per-account directory has to be created here: the CLIs assume
# an existing HOME and fail before the login prompt when it is missing.
mkdir -p "$target_home"
if [ "${1:-}" = "claude" ]; then
  mkdir -p "$target_home/.claude"
fi
chown -R nonroot:nonroot /data /auth /tmp/onwatch-runtime

exec gosu nonroot sh -c '
  set -eu

  export HOME="'$target_home'"
  export XDG_RUNTIME_DIR="/tmp/onwatch-runtime/antigravity/'$account'"
  mkdir -p "$XDG_RUNTIME_DIR"
  chmod 0700 "$XDG_RUNTIME_DIR"

  if [ -z "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
    export DBUS_SESSION_BUS_ADDRESS="$(dbus-daemon --session --fork --print-address=1)"
  fi

  # agy uses the Linux Secret Service for its encrypted OAuth session.
  # Starting it here makes both `onwatch` and the agy-login service operate
  # against the same container-local credential store.
  eval "$(gnome-keyring-daemon --start --components=secrets)"

  # Seeding the config once turns off session history, so the auth volume
  # accumulates no transcripts. See docs/WITH_USER_ENV.md#volume-reference for the
  # full list of files this volume ends up holding.
  export CODEX_HOME="'${target_codex_home:-/auth/codex/default}'"
  if [ ! -e "$CODEX_HOME/config.toml" ]; then
    mkdir -p "$CODEX_HOME"
    cat > "$CODEX_HOME/config.toml" <<CODEX_CONFIG
[history]
persistence = "none"
CODEX_CONFIG
  fi

  case "${1:-}" in
    claude)
      shift
      # Cooperate with the onWatch per-account OAuth transaction. `flock` works
      # across the daemon and the one-shot login containers because this lock
      # file lives in their shared claude-auth volume. Bounded, with a distinct
      # conflict code, so a stuck holder surfaces as a message rather than an
      # unexplained hang.
      status=0
      flock -w 60 -E 75 "$HOME/.claude/.credentials.json.lock" /usr/local/bin/claude "$@" || status=$?
      if [ "$status" -eq 75 ]; then
        echo "claude: timed out waiting for the credential lock; is onwatch mid-refresh?" >&2
      fi
      exit "$status"
      ;;
    agy|codex)
      cli="$1"
      shift
      exec "/usr/local/bin/$cli" "$@"
      ;;
  esac

  exec /app/onwatch "$@"
' -- "$@"
