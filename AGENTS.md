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
  file. `internal/application` is one flat package of use cases. Group
  adapters by responsibility under `internal/infrastructure/`; the active
  groups are `artifacts`, `caddy`, `config`, `plugins`, `security` and
  `storage`. Do not recreate the deleted legacy `network` or
  `observability` packages; user-traffic serving belongs to Caddy. Keep
  presentation limited to `api` and `cli`.
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
- Write the failing TypeScript test before its implementation, then implement
  the behavior until the test is green. Keep the temporary red state local:
  commit the test and implementation together only after the increment is green;
  never create a separate red-test commit or leave a red commit between changes.
- An increment is a completed vertical slice of approximately 30–150 lines of
  implementation together with its tests. Do not split work into micro-steps
  such as one JSON key or one assertion; keep the slice cohesive and usable.
- During development, run only the focused suite, for example
  `npx vitest run tests/<path>`. Do not run full gates at every micro-step.
  Once the increment is complete, run `make check`, `go vet ./...`, and
  `make staticcheck-u1000` exactly once at the end, before its single green
  commit. The candidate tree must contain only the completed increment, with no
  partial/red test state or unrelated changes. Fix any failures before starting
  another increment.
- Preserve the passing `contract-publication`, `arch-lint`, `dead-artifacts`,
  and `approved-orphan-symbols` gates. Do not weaken or rewrite these gates to
  accommodate unrelated refactoring. Add architecture tests only when a new
  functional task introduces a new architectural requirement; do not expand
  architecture coverage as a standalone task.
- Prioritize closing documented gaps in the roadmap and `TODO.md` over adding
  more coverage for behavior that is already implemented and green.
- Create exactly one commit per completed increment, with a message in the form
  `feat(core): <concise change>`; the commit must contain both the tests and
  implementation for that increment.
- `tests/unit/` exercises deterministic public behaviour through the CLI and
  controlled fixtures. `tests/integration/` runs the compiled Gateway process
  and covers HTTP, TCP, UDP, TLS, filesystem state, and Management API.
- Test contracts are fetched from `Liapoldus/liapoldus.github.io` `main` until
  Gateway v1 is complete, as agreed. A network failure must fail contract
  verification loudly rather than using an untracked fallback.
- Run the focused TS suite while implementing. At milestone completion,
  additionally run race checks and the applicable golden vectors; the full
  `make check`, vet, and staticcheck cadence is defined above.

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
