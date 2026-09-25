# AGENTS.md — Liapoldus Gateway core

## Purpose and contract

`core` implements the Liapoldus Gateway control plane and integrates the Caddy
data plane in Go. Public user traffic is served by embedded Caddy or a supervised
compatible external Caddy process; the Gateway Management API is not a traffic
proxy. Until Gateway v1 is complete, the canonical observable contract is the
documentation repository at
`/Users/docup/Projects/Liapoldus Engine/liapoldus.github.io`, especially
`public/spec/` and the Gateway documentation pages.

Do not silently resolve a contradiction or a missing rule in that contract.
Record the exact files and conflicting behaviour in `TODO.md` and ask for a
decision before implementing it.

The plugin transport contract does **not** belong in this repository. Its
source, protobuf definitions, generated types, gRPC transport API, and protocol
tests belong in `/Users/docup/Projects/Liapoldus Engine/pluginprotocol` and are
released as `github.com/Liapoldus/pluginprotocol`. Gateway imports that module
and owns process supervision and scoped grants. Caddy's Liapoldus handler
dispatches user capability traffic directly to the plugin; Gateway control
components prepare and synchronize immutable dispatch snapshots but must not
proxy user request/response bodies.

## Implementation rules

- Use Go 1.24 or newer. `internal/domain` contains exactly `models/` and
  `interfaces/`: models, port interfaces, typed errors and validating
  constructors only. Each domain model, typed error or interface has its own
  file. `internal/application`
  is one flat package of use cases. Group adapters in
  `infrastructure/config`, `network`, `security`, `storage`, `plugins` and
  `observability`; keep presentation limited to `api` and `cli`.
  `cmd/gateway` is the composition root.
- Use SQLite as the authoritative store for Gateway v1 control-plane metadata:
  groups/revisions/current/previous, plugin instances, service-key verifier
  metadata, operations/idempotency, audit and Caddy checkpoints. This is an
  explicit architecture decision; do not restore the former filesystem-only
  persistence design. Keep immutable Caddyfile/frontend/checkpoint artifacts
  as files under the configured artifact root. SQLite lives on local storage,
  uses migrations/foreign keys/WAL, and must recover atomically with artifacts.
- Never log, return, trace, or audit raw secrets, private keys, cookies,
  Authorization values, service keys, or grant handles.
- Do not introduce domain string literals in Go. YAML fields, commands, flags,
  environment names, paths, defaults, error codes, diagnostic text and JSON
  keys belong to versioned external contract files under `assets/contracts/`
  (mirroring the canonical interface from `liapoldus.github.io/public/spec/`).
  They are loaded through the root `core` contract adapter via `go:embed`; the
  `assets/` directory itself contains static files only. Go code
  may contain only import paths, the `//go:embed` asset directives, and
  identifiers bound to loaded contract values. Asset file names referenced by
  `assets.Contract` are treated as those embed directives and are the single
  allowed literal path strings. File-name and flag spellings that the contract
  itself must not change (tool flags, CLI display strings, JSON keys, error
  codes) are also hosted in the contract files; reach for them through the
  `config` package loaders instead of reproducing them in adapters.
- A failed compile/reload/publish must leave the active snapshot and release
  pointers unchanged.
- Target macOS and Linux. Keep Docker, GitHub Actions, and short operator
  documentation current with executable behaviour.

## Mandatory test-first workflow

- All test code lives under `tests/` and is TypeScript run by Vitest + tsx.
  Do not add Go `*_test.go` files or test helpers under production packages.
- For every implementation increment, create a separate **red test commit**
  first, then a minimal implementation commit that turns that increment green.
- `tests/unit/` exercises deterministic public behaviour through the CLI and
  controlled fixtures. `tests/integration/` runs the compiled Gateway process
  and covers HTTP, TCP, UDP, TLS, filesystem state, and Management API.
- Test contracts are fetched from `Liapoldus/liapoldus.github.io` `main` until
  Gateway v1 is complete, as agreed. A network failure must fail contract
  verification loudly rather than using an untracked fallback.
- Each increment must run its focused TS suite; milestone completion requires
  the full suite, race checks, and the applicable golden vectors.

## Architecture gate

- Run `go vet ./...` and `make check` before declaring a milestone complete.
  It builds Gateway, runs the TypeScript suite, and executes the architecture
  gate.
- `make arch-lint` runs `fe3dback/go-arch-lint` in Docker. Do not replace it
  with a host-installed binary: CI intentionally uses the same Docker image.
- The linter enforces allowed import directions. The TypeScript architecture
  suite additionally enforces the directory contract and one domain model or
  interface declaration per file.

## Contract ownership after v1

After all Gateway v1 vectors pass, contract ownership moves into `core`:
generate OpenAPI, config schema, runtime/error contracts and vectors from
code, publish them as versioned GitHub Release assets, and only then change
the documentation site to consume those release assets. Do not begin that
migration early.
