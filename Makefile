.DEFAULT_GOAL := check

.PHONY: build test test-ts arch-lint check

build:
	go build ./...

test:
	npm --prefix tests test

test-ts: test

arch-lint:
	docker run --rm -v "$(CURDIR):/app" fe3dback/go-arch-lint:latest-stable-release check --project-path /app

check: build test arch-lint
