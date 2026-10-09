.DEFAULT_GOAL := check

.PHONY: build test test-ts test-race docker-smoke arch-lint staticcheck-u1000 check

build:
	go build ./...

test:
	go test ./...
	npm --prefix tests test

test-ts: test

# Core has no Go *_test.go files by design, so `go test -race ./...` compiles
# nothing and would report success vacuously. The real race gate builds the
# service and fixtures under -race with halt_on_error=1, so a detected race
# aborts instead of printing a warning nobody reads.
test-race:
	npm --prefix tests test -- --run race-detection

docker-smoke:
	./scripts/docker-smoke.sh

arch-lint:
	docker run --rm -v "$(CURDIR):/app" fe3dback/go-arch-lint@sha256:9f4fb1216095becb37d889cc471ee8d93f7b5e5d531bde83571415013eca3fa1 check --project-path /app

staticcheck-u1000:
	GOTOOLCHAIN=go1.26.0 go tool staticcheck -checks=U1000 ./...

check: build test arch-lint
