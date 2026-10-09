# AGENTS.md — Liapoldus Core

## Purpose and source of truth

Core is the single-instance Core control plane and the durable source of
desired configuration. It stores versioned JSON in SQLite, builds immutable
in-memory snapshots, and exposes exact generations to plugins over the Plugin
SDK REST API. Core notifies each plugin replica with REST `Reload(generation)`;
the plugin pulls that generation from Core. Core does not serve public traffic
or implement product-specific data planes; those belong to independently
connected plugins.

The normative architecture, public Management API, environment bootstrap, public
error contract and Core implementation guides are owned by this repository in
`docs/site/core/`. The VitePress aggregator publishes those files while
preserving their public routes. The standalone
Plugin SDK in workspace directory `plugin-sdk/` is the owner of plugin-facing
REST lifecycle/configuration APIs and common plugin facilities; its Go module
path is `github.com/Liapoldus/plugin-sdk`. The sole owner of
plugin-to-plugin transport and generic peer
communication is
`/Users/docup/Projects/Liapoldus Engine/pluginprotocol`. Core uses the Plugin
SDK REST client only and must not import or call `pluginprotocol`. Do not copy
SDK or peer protocol contracts into Core except generated/mirrored build assets
explicitly required by the contract-publication check.

Core's runtime bootstrap is ENV-only: `CORE_SQLITE_PATH` selects SQLite and
`CORE_INIT_*` is consumed exactly once by Core itself during first startup. Core
creates the initial SQLite settings revision before opening the Management API;
there is no Core `init`, `serve`, database or inspection CLI. The standalone
`liapoldus` CLI may start Core and provide the bootstrap environment, but never
opens SQLite or imports Core internals. Runtime settings are stored in Core
SQLite and changed through the versioned Management API. Runtime YAML/JSON
loaders and static endpoint fallback are not supported. Replicas become eligible
through authenticated Plugin SDK registration and active leases. Deployment
automation, including local process lifecycle, belongs to the standalone CLI or
an operator-managed adapter; Core does not supervise or install workloads.

## Architecture and implementation rules

- Core owns generic plugin instance metadata, desired settings, authenticated
  replica observations and leases, durable operations, Core Management access and audit. Core v1
  does not own plugin-to-plugin interaction policies or interaction grants; that
  authorization surface is deferred to v2. This is distinct from scoped,
  one-use secret grants: Core must expose those through the Plugin SDK REST
  control API for opaque secret references used by a plugin's own configuration.
  Secret grants are not plugin-to-plugin permissions and must never be added to
  `pluginprotocol`. Keep the v1 grant API generic and bound to the authenticated
  replica, exact active generation, reference and purpose. Core must not contain
  plugin-name, capability-name, provider, or product-specific branches.
- In v2, generic caller→target peer-link policy is Core-owned desired state in
  SQLite and is authored through the authenticated Management API with CAS and
  audit. Do not put mutable policy values in bootstrap YAML or static contract
  assets. Publish policy only through the SDK-defined authenticated
  peer-directory long-poll; Core composes the directory but never proxies peer
  calls or interprets product contracts.
- SQLite is the only durable Core store for desired configuration and control
  metadata. Each plugin instance has exactly two durable configuration slots
  that are ever published to a plugin: `active` and `previous`. A third
  internal slot, `staging`, holds the validated candidate for a limited time
  so that promotion, rollback and interrupted-operation recovery are durable;
  it is never pullable by a replica. Store plugin configurations as raw JSON
  BLOBs; do not decode and re-encode them. Compute the generation digest over
  the exact stored UTF-8 bytes. Core may validate JSON syntax, size, duplicate
  keys, and a generic plugin-owned JSON Schema, but must not interpret
  product-specific fields. Runtime paths use immutable in-memory snapshots and
  never read configuration files. Core exposes an exact immutable
  JSON generation; plugins pull it from Core only after REST
  `Reload(generation)`. Plugins must not read application settings from
  environment variables, argv, or application config files.
- The operator owns Core/plugin binary provenance and process lifecycle in every
  version. Deployment tooling must not author product JSON or Core configuration generations. Core
  only connects to explicitly registered endpoints, applies state, and checks
  health. Core must not install, start, stop, restart, scale, or delete plugin
  processes/containers, and must not contain release installation,
  adoption/uninstall workflows, or container-provider control. Core is not a
  certificate authority. A replica's advertised release digest is compatibility
  metadata, not Core verification of the binary's provenance.
- `Reload(generation)`, exact-generation config retrieval and scoped secret
  redemption use the Plugin SDK REST API, not `pluginprotocol`. Rollback is a
  Core Management API operation that swaps `active`/`previous` and notifies
  replicas through ordinary `Reload`. After validating a candidate, promote it
  to desired `active` in the same SQLite transaction that moves former `active`
  to `previous` and discards the older `previous`, before notifying replicas.
  `Rollback` swaps `active` and `previous` before notifying replicas. Partial
  rollout is roll-forward: keep the promoted generation as desired, record
  per-replica acknowledgements, mark the instance degraded, and fence replicas
  that have not acknowledged that generation. Core v1 has no background retry
  loop; reconciliation after a Core/plugin restart may re-announce the same
  immutable generation, relying on the Plugin SDK's idempotent Reload contract.
  Do not replay a non-idempotent operation whose outcome is unknown. Secrets are never
  embedded in config JSON or returned/logged; scoped redemption uses the
  Plugin SDK REST API.
- `pluginprotocol` is a standalone, plugin-agnostic library for plugin-to-plugin
  communication only. It has no Core lifecycle/configuration methods and no
  product-specific capabilities. Its configurable physical transport must not
  change plugin-defined application endpoints. The separate Plugin SDK is a
  standalone Go module independent of `pluginprotocol`; all plugins use it for
  their common REST endpoints, configuration, health, metrics, logging, and
  error handling.
- Keep one lifecycle architecture: after a complete REST migration slice has
  passed its child-process conformance gate, remove the superseded Core↔plugin
  lifecycle transport and its fallback paths. Do not preserve two permanent
  lifecycle APIs or describe transitional compatibility as a supported mode.
- `internal/domain` contains exactly `models/` and `interfaces/`; each model,
  interface, and typed error belongs to a focused subject package. Only validating
  constructors and model validation are permitted there. `internal/application`
  groups related use cases in subject subpackages with concise filenames.
  `internal/infrastructure` contains technical adapters in
  focused subpackages. `internal/presentation` contains only the Management
  `api/` adapter and its documented subpackages. The Core executable composition
  root is `cmd/core`; it starts the runtime and API and contains no user-facing
  command dispatcher.
- SQL statements and migration history live as named parameterized Go definitions
  in storage adapters. Application and persistence failures use typed Go errors.
  Public error codes, field names, commands and diagnostics are code-owned;
  published schemas/OpenAPI/error catalogs are deterministic generated artifacts.
  ENV supplies bootstrap only; durable operational settings belong in SQLite.
  Do not expose
  driver errors, SQL text, paths, secrets, cookies, authorization values,
  private keys or grant handles.
- Do not add product-specific runtime, API, or state to Core, or introduce
  PostgreSQL/S3. Product data planes, including HTTP/TLS and L4 servers, belong
  exclusively to independently connected plugins; Core only manages generic
  plugin lifecycle and desired configuration.
- Target macOS and Linux. Keep build, CI and operator documentation aligned
  with the approved target; preserve unrelated dirty and untracked files.

## Mandatory test-first workflow

- Native Go unit/integration tests live alongside code; TypeScript under `tests/`
  covers frontend, HTTP/CLI and real child-process conformance. Preserve coverage
  before removing redundant fixture wrappers.
- Write the focused failing test before implementation. Keep the temporary
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
  vectors, macOS/Linux builds and manually deployed service smoke. Do not declare v1 ready while
  any required cross-component conformance gate remains open.

## Contract ownership

Code-owned models and declarations are canonical for generated OpenAPI, schemas,
public errors and vectors. Generated public contracts are deterministic build
artifacts. Protobuf remains the canonical wire source where applicable; do not
add a second generator for the same protocol.
