#!/bin/sh
set -eu

# Named Docker volumes are initially root-owned. The Antigravity CLI writes
# its profile and GNOME Keyring writes encrypted session state below HOME.
mkdir -p /data /home/nonroot/.gemini /home/nonroot/.local/share/keyrings /tmp/onwatch-runtime
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
  # Starting it here makes both `onwatch` and `docker compose run ... agy`
  # operate against the same container-local credential store.
  eval "$(gnome-keyring-daemon --start --components=secrets)"

  if [ "${1:-}" = "agy" ]; then
    shift
    exec /usr/local/bin/agy "$@"
  fi

  exec /app/onwatch "$@"
' -- "$@"
