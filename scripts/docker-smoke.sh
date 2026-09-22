#!/usr/bin/env sh
set -eu

image="${GATEWAY_DOCKER_IMAGE:-liapoldus-gateway:ci}"
docker build --tag "$image" .
docker run --rm "$image" --help >/dev/null
printf '%s\n' "gateway docker smoke: ok"
