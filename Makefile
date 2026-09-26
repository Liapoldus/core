.DEFAULT_GOAL := check

.PHONY: build test test-ts test-race docker-smoke arch-lint staticcheck-u1000 check

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

staticcheck-u1000:
	GOTOOLCHAIN=go1.26.0 go tool staticcheck -checks=U1000 ./...

check: build test arch-lint
