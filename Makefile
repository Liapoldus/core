.DEFAULT_GOAL := check

.PHONY: build test test-ts test-race docker-smoke arch-lint check

build:
	go build ./...

test:
	npm --prefix tests test

test-ts: test

test-race:
	go test -race ./...

docker-smoke:
	./scripts/docker-smoke.sh

arch-lint:
	docker run --rm -v "$(CURDIR):/app" fe3dback/go-arch-lint:latest-stable-release check --project-path /app

check: build test arch-lint
