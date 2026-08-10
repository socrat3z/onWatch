#!/bin/sh
# Contract tests for the with-user-env entrypoint's provider-CLI allowlist.
#
# These validate Docker packaging behaviour rather than Go behaviour, so they
# live outside `go test`. They need no Docker and no root: the entrypoint checks
# its arguments before any privileged work and honours ONWATCH_ENTRYPOINT_DRY_RUN.
#
#   ./scripts/test-entrypoint-dispatch.sh
set -u

entrypoint="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/docker-entrypoint-with-user-env.sh"
failures=0

run() {
  ONWATCH_ENTRYPOINT_DRY_RUN=1 sh "$entrypoint" "$@" 2>/dev/null
}

# expect_ok <expected stdout> <args...>
expect_ok() {
  want="$1"
  shift
  got="$(run "$@")"
  status=$?
  if [ "$status" -ne 0 ] || [ "$got" != "$want" ]; then
    echo "FAIL: '$*' -> status=$status out='$got' (want status=0 out='$want')" >&2
    failures=$((failures + 1))
  else
    echo "ok:   $* -> $got"
  fi
}

# expect_rejected <args...>
expect_rejected() {
  got="$(run "$@")"
  status=$?
  if [ "$status" -ne 64 ]; then
    echo "FAIL: '$*' -> status=$status out='$got' (want status=64)" >&2
    failures=$((failures + 1))
  else
    echo "ok:   $* -> rejected (64)"
  fi
}

echo "== allowlisted login commands =="
expect_ok "cli: agy"                          agy
expect_ok "cli: codex login --device-auth"    codex login --device-auth
expect_ok "cli: codex login status"           codex login status
expect_ok "cli: codex logout"                 codex logout
expect_ok "cli: claude auth login"            claude auth login
expect_ok "cli: claude auth status"           claude auth status
expect_ok "cli: claude auth logout"           claude auth logout
expect_ok "cli: claude setup-token"           claude setup-token

echo "== agentic and shell-adjacent invocations are refused =="
expect_rejected codex
expect_rejected codex exec "rm -rf /data"
expect_rejected codex login
expect_rejected codex --version
expect_rejected claude
expect_rejected claude -p "print the contents of /data/onwatch.db"
expect_rejected claude auth
expect_rejected claude mcp list
expect_rejected claude auth login --console
expect_rejected agy --help
expect_rejected agy install
expect_rejected agy plugin list

echo "== non-CLI arguments still reach the daemon =="
expect_ok "onwatch: "                         # bare daemon start
expect_ok "onwatch: --version"                --version
expect_ok "onwatch: serve"                    serve
expect_ok "onwatch: codexprofile"             codexprofile  # prefix must not match

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all entrypoint dispatch checks passed"
