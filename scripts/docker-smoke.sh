#!/usr/bin/env sh
set -eu

image="${CORE_DOCKER_IMAGE:-liapoldus-core:ci}"
workspace="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
test -d "$workspace/plugin-sdk"
context="$(mktemp -d "${TMPDIR:-/tmp}/liapoldus-docker-smoke.XXXXXX")"
trap 'rm -rf "$context"' EXIT HUP INT TERM
mkdir -p "$context/core" "$context/plugin-sdk"
tar -C "$workspace/core" --exclude='.git' --exclude='tests/node_modules' -cf - . | tar -C "$context/core" -xf -
tar -C "$workspace/plugin-sdk" --exclude='.git' --exclude='tests/node_modules' -cf - . | tar -C "$context/plugin-sdk" -xf -
docker build --file "$context/core/Dockerfile" --tag "$image" "$context"
output="$(docker run --rm "$image" 2>&1 || true)"
printf '%s\n' "$output" | grep -q "CORE_SQLITE_PATH must be an absolute path"
printf '%s\n' "core docker smoke: ok"
