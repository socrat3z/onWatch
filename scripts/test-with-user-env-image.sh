#!/usr/bin/env bash
# Smoke checks for the with-user-env image. Complements
# scripts/test-entrypoint-dispatch.sh, which needs no Docker.
#
#   ./scripts/test-with-user-env-image.sh            # host arch only
#   ./scripts/test-with-user-env-image.sh --all-arch # + a cross build for the other arch
#
# Set ONWATCH_SKIP_BUILD=1 when onwatch-with-user-env:test is already built
# (CI builds it once with buildx, then runs these checks against it).
#
# The cross build is build-only: it proves the TARGETARCH mapping resolves real
# artifacts and checksums on both architectures without needing qemu.
set -euo pipefail

# Git Bash / MSYS rewrites arguments that look like absolute Unix paths into
# Windows paths, which mangles every in-container path handed to docker.
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

cd "$(dirname "$0")/.."

image=onwatch-with-user-env:test
all_arch=0
[[ "${1:-}" == "--all-arch" ]] && all_arch=1

host_arch=$(docker version --format '{{.Server.Arch}}')
case "$host_arch" in
  amd64) other_arch=arm64 ;;
  arm64) other_arch=amd64 ;;
  *) echo "unsupported host arch: $host_arch" >&2; exit 1 ;;
esac

if [[ "${ONWATCH_SKIP_BUILD:-0}" == "1" ]]; then
  echo "== reusing prebuilt $image =="
  docker image inspect "$image" >/dev/null
else
  echo "== building runtime for linux/$host_arch =="
  docker build --target runtime -f Dockerfile.with-user-env -t "$image" .
fi

echo "== image size =="
docker image inspect "$image" --format '{{.Size}} bytes'

echo "== staged CLIs are the pinned native binaries =="
docker run --rm --entrypoint /opt/provider-cli/codex/bin/codex "$image" --version
docker run --rm --entrypoint /opt/provider-cli/claude "$image" --version
docker run --rm --entrypoint /usr/local/bin/agy "$image" --version

echo "== pruned artifacts stayed out of the runtime image =="
for path in /opt/bun /usr/local/bin/bun /usr/local/bin/node /usr/local/bin/npm \
            /opt/provider-cli/codex/bin/codex-code-mode-host; do
  if docker run --rm --entrypoint /bin/sh "$image" -c "test -e $path"; then
    echo "FAIL: $path present in runtime image" >&2
    exit 1
  fi
  echo "ok:   $path absent"
done

echo "== the daemon ends up as UID 65532 on PID 1 =="
# gosu replaces the root shell, and onwatch then replaces gosu, so PID 1 in a
# running container is onwatch itself under the nonroot account.
docker run --rm "$image" --version >/dev/null
cid=$(docker run -d --rm "$image")
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT
for _ in 1 2 3 4 5 6 7 8 9 10; do
  pid1_uid=$(docker exec "$cid" stat -c %u /proc/1 2>/dev/null || true)
  [[ "$pid1_uid" == "65532" ]] && break
  sleep 1
done
if [[ "${pid1_uid:-}" != "65532" ]]; then
  echo "FAIL: PID 1 runs as uid ${pid1_uid:-unknown}, want 65532" >&2
  exit 1
fi
echo "ok:   PID 1 runs as UID 65532"
docker rm -f "$cid" >/dev/null
trap - EXIT

echo "== login commands are allowlisted, agentic ones are not =="
docker run --rm "$image" codex login status >/dev/null 2>&1 || true
docker run --rm "$image" claude auth status >/dev/null 2>&1 || true
echo "ok:   status commands dispatched (exit code reflects 'not logged in')"

for bad in "codex exec pwd" "claude -p hi" "agy install"; do
  # shellcheck disable=SC2086
  if docker run --rm "$image" $bad >/dev/null 2>&1; then
    echo "FAIL: '$bad' was not rejected" >&2
    exit 1
  fi
  echo "ok:   '$bad' rejected"
done

echo "== credential dirs are writable by UID 65532 =="
docker run --rm --user 65532 --entrypoint /bin/sh "$image" -c \
  'set -e; for d in ~/.gemini ~/.codex ~/.claude ~/.local/share/keyrings; do touch "$d/.probe" && rm "$d/.probe"; done'
echo "ok:   all four credential dirs writable"

if [[ "$all_arch" -eq 1 ]]; then
  echo "== cross build for linux/$other_arch (checksum + TARGETARCH mapping) =="
  docker buildx build --platform "linux/$other_arch" --target runtime \
    -f Dockerfile.with-user-env --load=false .
  echo "ok:   linux/$other_arch resolves and verifies"
fi

echo
echo "all with-user-env image checks passed"
