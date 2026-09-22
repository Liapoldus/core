#!/usr/bin/env sh
set -eu

image="${GATEWAY_DOCKER_IMAGE:-liapoldus-gateway:ci}"
docker build --tag "$image" .
output="$(docker run --rm "$image" 2>&1 || true)"
printf '%s\n' "$output" | grep -q "ожидается команда"
printf '%s\n' "gateway docker smoke: ok"
