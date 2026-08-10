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

# Named Docker volumes are initially root-owned. The Antigravity CLI writes its
# profile, Codex writes auth.json, Claude Code writes .credentials.json, and
# GNOME Keyring writes encrypted session state - all below HOME.
mkdir -p /data /home/nonroot/.gemini /home/nonroot/.codex /home/nonroot/.claude \
  /home/nonroot/.local/share/keyrings /tmp/onwatch-runtime
chown -R nonroot:nonroot /data /home/nonroot /tmp/onwatch-runtime

exec gosu nonroot sh -c '
  set -eu

  export HOME=/home/nonroot
  export XDG_RUNTIME_DIR=/tmp/onwatch-runtime
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
  export CODEX_HOME="${CODEX_HOME:-$HOME/.codex}"
  if [ ! -e "$CODEX_HOME/config.toml" ]; then
    mkdir -p "$CODEX_HOME"
    cat > "$CODEX_HOME/config.toml" <<CODEX_CONFIG
[history]
persistence = "none"
CODEX_CONFIG
  fi

  case "${1:-}" in
    agy|codex|claude)
      cli="$1"
      shift
      exec "/usr/local/bin/$cli" "$@"
      ;;
  esac

  exec /app/onwatch "$@"
' -- "$@"
