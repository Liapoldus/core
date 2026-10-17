# AGENTS.md — Liapoldus Core

## Purpose and source of truth

Core is the single-instance Core control plane and the durable source of
desired configuration. It stores versioned JSON in SQLite, builds immutable
in-memory snapshots, and exposes exact generations to services through the
Service SDK REST+mTLS adapter. Core notifies each service replica with
`Reload(generation)`; the service pulls that exact generation through the
selected adapter. Core does not serve public traffic or implement
product-specific data planes; those belong to independently connected services.

The normative architecture, public Management API, environment bootstrap, public
error contract and Core implementation guides are owned by this repository in
`docs/site/core/`. The VitePress aggregator publishes those files while
preserving their public routes. The standalone
Service SDK in workspace directory `service-sdk/` is the owner of service-facing
REST lifecycle/configuration APIs and common service facilities; its Go module
path is `github.com/Liapoldus/service-sdk/v2`. The sole owner of
service-to-service transport and generic peer
communication is
`/Users/docup/Projects/Liapoldus Engine/wool`. Core may use the
Service SDK REST client, but must not
import or call `wool` for lifecycle/configuration. Do not copy
SDK or peer protocol contracts into Core except generated/mirrored build assets
explicitly required by the contract-publication check.

Core's runtime bootstrap is API-only at the command surface: `CORE_SQLITE_PATH`
selects SQLite and secure `CORE_INIT_*` references are consumed once by the
runtime before opening the Management API. The standalone `liapoldus` CLI owns
local process lifecycle, backup/restore, migration and remote API calls; it
never imports Core internals or provides a second Core command dispatcher.
Core has no user-facing command dispatcher, migration binary or database CLI.
Runtime YAML/JSON loaders and static endpoint fallback are not supported.
Replicas become eligible through authenticated Service SDK registration and active
leases. Core does not supervise or install workloads.

## Architecture and implementation rules

- Core owns generic service instance metadata, desired settings, authenticated
  replica observations and leases, durable operations, Core Management access
  and audit, and generic service-to-service peer-link policy. This is distinct
  from scoped,
  one-use secret grants: Core must expose those through the Service SDK REST
  control API for opaque secret references used by a service's own configuration.
  Secret grants are not service-to-service permissions and must never be added to
  `wool`. Keep the grant API generic and bound to the authenticated
  replica, exact active generation, reference and purpose. Core must not contain
  service-name, capability-name, provider, or product-specific branches.
- In v3, generic caller→target peer-link policy is Core-owned desired state in
  SQLite and is authored through the authenticated Management API with CAS and
  audit. Do not put mutable policy values in bootstrap YAML or static contract
  assets. Publish policy only through the SDK-defined authenticated
  peer-directory long-poll; Core composes the directory but never proxies peer
  calls or interprets product contracts.
- SQLite is the only durable Core store for desired configuration and control
  metadata. Each service instance has exactly two durable configuration slots
  that are ever published to a service: `active` and `previous`. A third
  internal slot, `staging`, holds the validated candidate for a limited time
  so that promotion, rollback and interrupted-operation recovery are durable;
  it is never pullable by a replica. Store service configurations as raw JSON
  BLOBs; do not decode and re-encode them. Compute the generation digest over
  the exact stored UTF-8 bytes. Core may validate JSON syntax, size, duplicate
  keys, and a generic service-owned JSON Schema, but must not interpret
  product-specific fields. Runtime paths use immutable in-memory snapshots and
  never read configuration files. Core exposes an exact immutable
  JSON generation; REST services pull it from Core after REST `Reload(generation)`;
  Services must not read application settings from
  environment variables, argv, or application config files.
- The operator owns Core/service binary provenance and process lifecycle in every
  version. Deployment tooling must not author product JSON or Core configuration generations. Core
  only connects to explicitly registered endpoints, applies state, and checks
  health. Core must not install, start, stop, restart, scale, or delete service
  processes/containers, and must not contain release installation,
  adoption/uninstall workflows, or container-provider control. Core is not a
  certificate authority. A replica's advertised release digest is compatibility
  metadata, not Core verification of the binary's provenance.
- `Reload(generation)`, exact-generation config retrieval and scoped secret
  redemption use the Service SDK REST lifecycle adapter, not
  `wool`. Rollback is a
  Core Management API operation that swaps `active`/`previous` and notifies
  replicas through ordinary `Reload`. After validating a candidate, promote it
  to desired `active` in the same SQLite transaction that moves former `active`
  to `previous` and discards the older `previous`, before notifying replicas.
  `Rollback` swaps `active` and `previous` before notifying replicas. Partial
  rollout is roll-forward: keep the promoted generation as desired, record
  per-replica acknowledgements, mark the instance degraded, and fence replicas
  that have not acknowledged that generation. Core has no background retry
  loop; reconciliation after a Core/service restart may re-announce the same
  immutable generation, relying on the Service SDK's idempotent Reload contract.
  Do not replay a non-idempotent operation whose outcome is unknown. Secrets are never
  embedded in config JSON or returned/logged; scoped redemption uses the
  Service SDK REST API.
- `wool` is a standalone, service-agnostic library for service-to-service
  communication only. It has no Core lifecycle/configuration methods and no
  product-specific capabilities. Its configurable physical transport must not
  change service-defined application endpoints. The separate Service SDK is a
  standalone Go module independent of `wool`; all services use it for
  their common REST endpoints, configuration, health, metrics, logging, and
  error handling.
- Core calls Services exclusively through Service SDK REST+mTLS. Wool belongs
  to independent service-to-service data planes. StudioPlugins are editor
  extensions only. Control-plane clients follow Studio → CLI → Core → Service SDK.
- `internal/domain` contains exactly `models/` and `interfaces/`; each model,
  interface, and typed error belongs to a focused subject package. Only validating
  constructors and model validation are permitted there. `internal/application`
  groups related use cases in subject subpackages with concise filenames.
  `internal/infrastructure` contains technical adapters in focused subpackages.
  `internal/presentation` contains only the Management `api/` adapter and its
  documented subpackages. `internal/runtime` owns server composition. The Core
  executable composition root is `cmd/core`; it starts the runtime and API and
  contains no user-facing command dispatcher.
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
  exclusively to independently connected services; Core only manages generic
  service lifecycle and desired configuration.
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
  API, SQLite recovery, filesystem state and service child-process behavior.
- Until the Core v2 contract is complete, contract tests fetch the canonical documentation
  contracts from `Liapoldus/liapoldus.github.io` `main`; network failure must
  fail verification rather than silently use an untracked fallback.

## Verification

- Run `go vet ./...` and `make check` before declaring an increment complete.
  `make check` builds Core, runs the TypeScript suite and executes architecture
  lint.
- `make arch-lint` uses the repository's Docker image as CI does; do not replace
  it with a host-installed linter.
- At milestone completion, also run applicable race checks, executable golden
  vectors, Linux/macOS/Windows builds and manually deployed service smoke. Do
  not declare Core v3 ready while any required cross-component conformance gate
  remains open.

## Contract ownership

Code-owned models and declarations are canonical for generated OpenAPI, schemas,
public errors and vectors. Generated public contracts are deterministic build
artifacts. Protobuf remains the canonical wire source where applicable; do not
add a second generator for the same protocol.

## Studio configurator boundary

The workspace-approved target is [`../studio/plan.md`](../studio/plan.md). Core
may expose service-owned live manifest/schema/readiness/status and typed
configuration/resource operations to the CLI. Treat declarative Studio UI
metadata as opaque service-owned metadata: Core validates bounded JSON and
authorization/ownership, but does not interpret widgets or product fields.
Studio never calls Core directly; the path remains Studio → typed CLI → Core.
Resources and restart-required settings must use explicit versioned Core/CLI
operations and preserve desired/effective/applied state. Do not add Docker,
Studio renderer, Server, or Echo product behavior to Core.
