# AGENTS.md — Liapoldus Gateway Core

## Purpose and source of truth

Core is the single-instance Gateway control plane. It stores desired service
configuration in SQLite, builds immutable in-memory snapshots, and pushes
versioned JSON to plugins through `pluginprotocol.ConfigApply`. Core does not
serve public traffic or implement product-specific data planes; those belong to
independently connected plugins.

Until Gateway v1 is complete, the normative architecture, public Management
API, bootstrap schema and public error contract live in
`/Users/docup/Projects/Liapoldus Engine/liapoldus.github.io`. The sole owner of
plugin protobuf, gRPC API, lifecycle payloads, and plugin JSON contracts is
`/Users/docup/Projects/Liapoldus Engine/pluginprotocol`. Do not copy those
contracts into Core except generated/mirrored build assets explicitly required
by the contract-publication check.

If a missing or contradictory requirement affects implementation, record the
specific conflict in `TODO.md` and ask the user before changing observable
behavior. The approved target is one global profile per Core: `supervised` or
`external`; these profiles are not mixed inside one Core instance.

## Architecture and implementation rules

- Core owns generic plugin instance metadata, desired settings, endpoint sets,
  interaction policy, grants, operations, access and audit. It must not contain
  plugin-name, capability-name, provider, or product-specific branches.
- SQLite is the only durable Core store for desired configuration and control
  metadata. Candidate and active revisions are durable; runtime request paths
  use immutable in-memory snapshots and never read SQLite or configuration files.
  Plugin settings are JSON pushed by Core; plugins do not pull them or read
  application settings from environment variables, argv, or application config
  files.
- In `supervised`, Core verifies TUF-signed package metadata and supervises
  local plugin processes. In `external`, an operator owns process lifecycle and
  Core only connects to configured endpoints, applies state, and checks health.
  Core is not a certificate authority.
- `ConfigApply` and `DispatchApply` are protocol-owned typed operations. Commit
  a new active generation only after the required replica-bound acknowledgments;
  do not replay a Call with an unknown outcome. Secrets are never embedded in
  config JSON or returned/logged; use scoped protocol grants.
- `internal/domain` contains exactly `models/` and `interfaces/`; each model,
  interface, and typed error has its own file. Only validating constructors and
  model validation are permitted there. `internal/application` remains a flat
  use-case package. `internal/infrastructure` contains technical adapters in
  focused subpackages. `internal/presentation` contains only `api/` and `cli/`
  and their documented adapter subpackages. `cmd/gateway` is the composition
  root.
- SQL statements live in source-owned `.sql` files embedded at build time;
  application and persistence failures use typed Go errors. Public error codes,
  JSON shapes, command/flag spellings, defaults and user-visible diagnostics
  remain versioned external contracts under `assets/contracts/`. Do not expose
  driver errors, SQL text, paths, secrets, cookies, authorization values,
  private keys or grant handles.
- Do not add product-specific runtime, API, or state to Core, or introduce
  PostgreSQL/S3. Product data planes, including HTTP/TLS and L4 servers, belong
  exclusively to independently connected plugins; Core only manages generic
  plugin lifecycle and desired configuration.
- Target macOS and Linux. Keep build, CI and operator documentation aligned
  with the approved target; preserve unrelated dirty and untracked files.

## Mandatory test-first workflow

- All Core test code lives under `tests/` and is TypeScript run by Vitest + tsx.
  Do not add Go `*_test.go` files or test helpers under production packages.
- Write the failing TypeScript test before implementation. Keep the temporary
  red state local; commit the test and implementation together only after the
  increment is green. Never make a red-test-only commit or leave a red commit
  between changes.
- An increment is a complete vertical slice, normally about 30–150 lines of
  implementation together with focused tests. Do not split a usable change into
  micro-steps such as one JSON key or one assertion.
- During development run only the focused suite, for example
  `npx vitest run tests/<path>`. Do not run the full gate at every micro-step.
  At the end of each completed increment, run `make check`, `go vet ./...`, and
  `make staticcheck-u1000` once on the clean candidate tree, before the single
  green commit for that increment. Fix failures before beginning another
  increment.
- Preserve passing `contract-publication`, `arch-lint`, `dead-artifacts`, and
  `approved-orphan-symbols` gates. Do not weaken or rewrite them for unrelated
  changes. Add architecture tests only when a functional task adds a new
  architectural requirement.
- Prioritize documented gaps in the roadmap and this `TODO.md` over expanding
  coverage of behavior that is already implemented and green.
- Use one commit per completed increment, with a message in the form
  `feat(core): <concise change>`. A commit contains both tests and implementation.
- `tests/unit/` covers deterministic public behavior through CLI/controlled
  fixtures. `tests/integration/` covers the compiled Core process, Management
  API, SQLite recovery, filesystem state and plugin child-process behavior.
- Until v1 is complete, contract tests fetch the canonical documentation
  contracts from `Liapoldus/liapoldus.github.io` `main`; network failure must
  fail verification rather than silently use an untracked fallback.

## Verification

- Run `go vet ./...` and `make check` before declaring an increment complete.
  `make check` builds Core, runs the TypeScript suite and executes architecture
  lint.
- `make arch-lint` uses the repository's Docker image as CI does; do not replace
  it with a host-installed linter.
- At milestone completion, also run applicable race checks, executable golden
  vectors, macOS/Linux builds and Docker smoke. Do not declare v1 ready while
  any required cross-component conformance gate remains open.

## Contract ownership after v1

Only after Gateway v1 gates pass may contract ownership move into Core: generate
OpenAPI, bootstrap schemas, public errors and vectors from code, publish them as
versioned release assets, and then change the documentation site to consume
those assets. Do not start that migration early.
