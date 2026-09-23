#!/usr/bin/env sh
set -eu

image="${GATEWAY_DOCKER_IMAGE:-liapoldus-gateway:ci}"
workspace="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
test -d "$workspace/pluginprotocol"
context="$(mktemp -d "${TMPDIR:-/tmp}/liapoldus-docker-smoke.XXXXXX")"
trap 'rm -rf "$context"' EXIT HUP INT TERM
mkdir -p "$context/core" "$context/pluginprotocol"
tar -C "$workspace/core" --exclude='.git' --exclude='tests/node_modules' -cf - . | tar -C "$context/core" -xf -
tar -C "$workspace/pluginprotocol" --exclude='.git' --exclude='tests/node_modules' -cf - . | tar -C "$context/pluginprotocol" -xf -
docker build --file "$context/core/Dockerfile" --tag "$image" "$context"
output="$(docker run --rm "$image" 2>&1 || true)"
printf '%s\n' "$output" | grep -q "ожидается команда"
printf '%s\n' "gateway docker smoke: ok"
