# Core v3 — production readiness

## Candidate-scoped secret validation — current checkpoint — 2026-10-19

- Core now persists changed candidates in the private staging slot before
  issuing generation-scoped secret grants. Bundle apply validates every
  candidate before scheduling promotion; a rejected candidate leaves the
  active generation unchanged. The source also maps candidate-validation
  failures to safe typed outcomes instead of reporting them as revision
  conflicts. Project files remain CLI-owned.
- The latest Core source commit is `43b7acc35bd2c640b08b769a335b127581190cdd`.
  Its clean Linux/arm64 candidate is
  `/tmp/liapoldus-core-43b7acc-linux-arm64`, SHA-256
  `5ccf6e176dd9784b4deccb5c033653646b253f66a716ebf0032b27c9c2ee7af8`;
  metadata reports `vcs.modified=false`. `make check`, `go vet ./...`,
  `make staticcheck-u1000` and the real Domain integration test passed. This
  candidate is not installed in the local target.
- Live local reads on 2026-10-19 show Core digest
  `df625da7910770c082db1364c0f9f0d149a15d487383af4a43e8aa8fb7607420`, a
  valid working-tree plan at digest
  `9c1d1ade4026d0ee87827c518d863f37e0ea829fd4638a7eb0ff964d565bffa8`, and
  Server 1.1.12 ready at generation 96. Project settings retain `/echo` and a
  temporary auth route referencing one active local secret; the catalog has
  two active and three revoked references, with no pending registration or
  rotation. `/echo` returns HTTP 200 and the candidate route returns HTTP 404.
  Secret values and opaque references were not printed.
- The previous live apply returned HTTP 409 and the safe log filter found a
  `grantDenied` event; the route is still unapplied. The Core response therefore
  needs rechecking with the new typed-error build after it is installed.
- The current Server candidate is 1.1.14 from commit `80ffb234263aba13fe69722b5d8423650d78b026`,
  SHA-256 `be991dab5b250461963ef46ad713fdbb3b1f2322915376c0ec0519623998a74e`.
  The user approved the earlier 1.1.13 digest, but that artifact is superseded
  and will not be used for acceptance. Exact action-time approval is pending
  for the current Core and Server digests, scoped only to `Server Echo Studio`
  / `local`; production is out of scope.

## Local apply admission and runtime recovery — superseded checkpoint — 2026-10-19

- Installed and ran the exact approved Linux/arm64 Core build from commit
  `0ae119b`, SHA-256
  `df625da7910770c082db1364c0f9f0d149a15d487383af4a43e8aa8fb7607420`, after
  backing up the local target registry, previous Core binary and SQLite state.
  The local Core container is running that digest.
- The approved CLI commit `d0191b8` is installed at
  `/Users/docup/go/bin/liapoldus`, SHA-256
  `1381dd4239795af2b64f9a1878d623eaa0de5c162d2d9895d32f018e010f00dd`. Its
  `dev apply` path always fetches Core's typed plan before secret staging and
  recovery persistence, including when the caller supplies an idempotency key.
  Unit tests cover stopping on `blockedBy` before staging, journal writes or
  Apply; full CLI checks and race tests pass.
- Typed `dev plan` for working-tree digest
  `c0ed4230629e8d35d3c725e6db1a8d44cfaa64c733351c4d3d62ddea8f2a2bac` is valid
  and reports Echo/Server at current revision 96, with no `blockedBy` items.
  The earlier HTTP 409 conflict has not been reproduced after the Core update
  and service registration; this does not identify which prior admission race
  caused it.
- `runtime start --revision WORKING_TREE --target local` reconciled stale Echo
  and Server containers, re-registered both replicas, and both reported ready
  with desired/applied generation 96. Server 1.1.11 and Echo 1.2.2 acknowledged
  their exact Project settings digests. `GET /echo?text=local-smoke` returned
  HTTP 200; omission of the required query returned HTTP 400 as specified.
- The Project has one existing `/echo` route and no secret references. Three
  prior synthetic Server references remain revoked. A new local auth secret is
  awaiting manual entry in Studio's secure field; no value has been entered by
  the agent. Successful live auth, scoped grant, rotation, replica ACK and
  retirement checks remain open. Production is out of scope.
- The user subsequently reported that the value was saved. A fresh Studio
  accessibility/screenshot read still shows an empty secure field and disabled
  Save button; typed `service secret list` still returns only the same three
  revoked references, with no active item or pending registration/rotation.
  Creation is therefore unconfirmed; no new reference is in the Core catalog.
- Re-ran `PATH=/Users/docup/.cache/codex-runtimes/codex-primary-runtime/
  dependencies/node/bin:$PATH GOWORK=off GOFLAGS=-p=1 make check`: 96 Vitest
  files passed, 1 skipped / 159 tests passed; Go build/tests, TypeScript
  typecheck/lint and Docker architecture lint passed. The real mTLS fixture
  `npm --prefix tests test -- integration/service-secret-grants.test.ts`
  passed 1/1. These are source/process-fixture checks, not live Server grant
  delivery.

## Local secret rotation smoke — superseded status — 2026-10-18

- The earlier missing-bootstrap-trust blocker below has been resolved after
  explicit local-only approval: the old local trust assets were backed up, a
  new local CA and target/service certificates were issued, and Core was
  restarted. The approved Darwin/arm64 CLI `68db41f10d7b566d…` is installed.
- Fresh typed reads report Core, Echo and Server containers running. Server
  1.1.11 is ready and acknowledged at generation 95; its active settings
  envelope still reports schemaVersion 1. The existing `/echo` request returns
  HTTP 200.
- The Core catalog contains two active synthetic Server references, one
  revoked record and a staged rotation. The Project file still has only
  `/echo`; neither active reference has been delivered to Server, and no auth
  route/configuration was applied. Do not infer grant delivery from the catalog
  or source-level mTLS tests.
- Studio fixed secret-reference validation in commit `b92c2d9` and pushed it.
  The newly built host digest `88a4d2b0939f2427…` has not yet been launched;
  its exact-digest action-time approval is pending. After that gate, complete
  the WebView selection, saved candidate, Server grant, authenticated HTTP,
  replica ACK and old-reference retirement checks. Remove the temporary route
  and revoke only the smoke references after runtime no longer uses them.
  Production is out of scope.

## Full owner gates re-run — 2026-10-18

- `PATH=/Users/docup/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:$PATH GOTOOLCHAIN=go1.26.0 GOWORK=off GOFLAGS=-p=1 make check` passed: Go build/tests, 96 Vitest files passed / 1 skipped, 159 tests passed, TypeScript typecheck, ESLint, and Docker architecture lint.
- `GOTOOLCHAIN=go1.26.0 GOWORK=off GOFLAGS=-p=1 go vet ./...` and `GOTOOLCHAIN=go1.26.0 GOWORK=off GOFLAGS=-p=1 make staticcheck-u1000` both passed.
- These source gates did not resolve the initial local runtime failure recorded
  below; the trust assets were later replaced under explicit local-only
  approval. See the current smoke checkpoint above for the present runtime
  state.

## Initial local Core bootstrap retry (historical) — 2026-10-18

- Studio's approved local Run retry reached CLI Core bootstrap after OrbStack
  exposed Docker Engine 29.8.2, then stopped before Core container creation:
  `Core Management TLS certificate is unavailable`. Typed
  `liapoldus runtime status --target local` reports Core exited.
- The local target profile references missing management/control certificate
  and key files plus missing management-client and replica CAs. No Core API,
  configuration validation, secret grant, or replica ACK occurred in this
  attempt; the staged catalog state was not changed by the failed bootstrap.
- Core TLS/CA material was not regenerated. The architecture decision keeps
  PKI renewal separate from application-secret rotation and forbids automatic
  CA replacement; restore the existing local trust bundle before further
  runtime acceptance.

## Staged local secret rotation before trust recovery (historical) — 2026-10-18

- The authorized local synthetic rotation is staged in the Core reference
  catalog: two active opaque references, one revoked record, and one pending
  rotation in `staged` state. This supersedes the no-pending-rotation snapshot
  below. Core stores the bounded reference/rotation metadata; the CLI OS vault
  holds the secret values.
- Runtime status reports the local Core container running and Echo/Server
  containers exited. The latest typed `service status server` call returned
  HTTP 503 `service_unavailable` because live service metadata is unavailable.
  The last successful readiness read reported Server 1.1.11, generation 94,
  ready and acknowledged.
- Project `services/server/settings.json` still contains only `/echo` and no
  secret reference. No reference has been delivered to Server; the active
  settings digest/configuration was not changed by staging the rotation.
- Studio Editor secret listing works, but reference selection on the approved
  host failed because its secret setter corrupted a protected route-array patch.
  Studio fixed this by writing the reference through the validated array
  operation stream. The new host build has not been launched yet; its exact
  digest requires the Studio action-time approval. No production target was
  accessed.
- Local rotation ACK, replacement authentication, restart recovery and old
  reference retirement remain unverified. Previously passing Core mTLS grant
  tests are fixture evidence only, not live delivery/ACK acceptance.

## Current local secret route and runtime verification — 2026-10-18

- The Studio-selected CLI at `/Users/docup/go/bin/liapoldus` (SHA-256
  `c33d2114808708be062fb23877da3d48a8c803203ccd3d6be06d2be1227082ae`)
  successfully reads the Server secret catalog from target `local`. The result
  is a valid `service-secret-references/v1` catalog containing one active,
  temporary test reference and one revoked record, with no pending
  registrations or rotations. The active reference remains unselected and is
  absent from Project settings and runtime configuration.
  This supersedes older live notes below reporting an empty catalog or HTTP 404.
- The same typed CLI reports Server 1.1.11 at generation 94, ready and
  acknowledged. Docker reports the Core process path as `/liapoldus/core`,
  bound read-only to the local candidate; hashing that mounted host binary
  confirms the authorized Linux/arm64 Core digest
  `e336a935e6e81ca44b5dbfb77d3f31ae6fd86e365ba203371e2e34f389b0588f`.
- Rechecked with `liapoldus service secret list --target local --service server`,
  `liapoldus service status server --target local`, and `liapoldus runtime status
  --target local` on 2026-10-18. These status/catalog reads changed no Core
  database, Project, service grant or runtime configuration. A separately
  authorized synthetic secret create/list succeeded through the local CLI, but
  the reference has not been delivered to Server. HTTP auth, rotation,
  replica-ACK gating, revoke, timeout and restart recovery remain unverified
  against the live target.
- The Studio source fix for `bridge.secret_path_not_declared` is committed and
  pushed; the rebuilt host has not yet been launched. Until that WebView gate is
  cleared, the active temporary reference remains unselected. Production is
  explicitly out of scope.
- The candidate section immediately below records its pre-install state. The
  read-only Docker mount inspection above now verifies that exact `e336…` binary
  is running; the historical 404 and pending-install text below no longer
  describe current local state.
- Focused source checks re-run on 2026-10-18:
  `GOTOOLCHAIN=go1.26.0 GOWORK=off GOFLAGS=-p=1 go test -p 1
  ./internal/application ./internal/presentation/api
  ./internal/infrastructure/storage ./internal/infrastructure/security -run
  'ServiceSecret|SecretReference' -count=1` passes all four packages;
  `PATH=/Users/docup/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:$PATH
  npm --prefix tests test -- integration/service-secret-grants.test.ts` passes
  1/1 real mTLS fixture. These do not exercise live creation or retirement in
  the selected target.

## Initial local secret catalog read-path evidence — 2026-10-18

- The exact Darwin/arm64 CLI candidate
  `5e30a42a1bcf5af1d3c6ac4131fa71a109ca3c234650e31443eaf28ef9ce8829` now
  reads `GET /api/services/{id}/secrets` through the local Core Management API
  for both Server and Echo. Both responses are valid empty catalogs with no
  pending registrations or rotations. This supersedes older TODO checkpoints
  reporting HTTP 404 for the route.
- The typed CLI status output does not expose the running Core binary digest,
  so this read-path check does not prove which Core artifact is active. It also
  does not prove create/revoke writes, mTLS grant scope, rotation ACK/timeout,
  or restart recovery. No Core database, Project, runtime or secret state was
  changed by this check.
- Re-ran `PATH=/Users/docup/.cache/codex-runtimes/codex-primary-runtime/
  dependencies/node/bin:$PATH npm --prefix tests test --
  integration/service-secret-grants.test.ts`: 1/1 real mTLS grant integration
  test passes, including the SQLite plaintext-marker scan. This is a fixture
  integration, not a deployment of the local Core candidate.

## Exact-source current Core candidate — 2026-10-18

- Built Linux/arm64 Core from published source HEAD
  `2e65f683026f91cb2c3cb0a93299d6f883432247` to
  `/tmp/liapoldus-core-20261018-2e65f68`, SHA-256
  `e336a935e6e81ca44b5dbfb77d3f31ae6fd86e365ba203371e2e34f389b0588f`.
  `file` confirms static ELF aarch64; `go version -m` records the exact
  revision and `vcs.modified=false`.
- On this HEAD, `make check`, `go vet ./...` and `make staticcheck-u1000` pass.
  The focused mTLS secret-grant integration passes and scans SQLite database,
  WAL and SHM for the secret marker.
- At the time of this candidate checkpoint it had not been installed; that
  pre-install state is superseded by the current local verification above.

## SQLite plaintext exclusion in secret-grant integration — 2026-10-18

- The real Core → Service SDK mTLS grant fixture now closes SQLite and scans
  `core.db`, `core.db-wal` and `core.db-shm` for the redeemed secret marker.
  It proves that the value remains outside Core's durable catalog/config store;
  only the reference and scoped metadata are persisted.
- Focused integration: `vitest run --maxWorkers=2
  tests/integration/service-secret-grants.test.ts` passes (1/1). Full `make
  check` passes (96 files passed, 1 skipped, 159 tests), including Go build/tests,
  TS typecheck/lint and Docker `arch-lint`; `go vet ./...` and
  `make staticcheck-u1000` pass.
- This changes only the acceptance fixture and expected output. No running Core,
  Project, catalog, credential or service runtime state was touched.

## Exact-source local candidate for current secret catalog — 2026-10-18

- Built Linux/arm64 Core from published source HEAD `eda44c95016c719e49968d3b76374908ff391f52` to `/tmp/liapoldus-core-current` with `GOTOOLCHAIN=go1.26.0 GOWORK=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /tmp/liapoldus-core-current ./cmd/core`.
- SHA-256: `aa49ff325f9374264edcb4927d199556ca3525a70926c92f08c90b1b7c93f626`. `file` reports a static ELF aarch64 executable; `go version -m` records the exact revision and `vcs.modified=false`.
- Current-source owner checks are recorded under the configuration submission/recovery checkpoint. The running local containers are older than this candidate; the candidate has not been installed. Secret-catalog, grant, rotation-timeout and restart acceptance against the running Core therefore remain open pending the normal exact-digest Studio flow.
- Fresh runtime-boundary verification passed: `./node_modules/.bin/vitest run --maxWorkers=2 integration/service-secret-grants.test.ts` (1/1). The fixture starts Core grant services with SQLite-backed metadata, an HTTPS+mTLS listener, a verified replica certificate and the real Service SDK broker; it checks exact generation/reference/purpose/replica scope and single redemption.

## Current local secret-catalog candidate — 2026-10-18

- The Core service-secret catalog and scoped replica grants are present in
  source commit `e35e66e`. The Linux/arm64 candidate
  was built from this exact HEAD at
  `/tmp/liapoldus-core-linux-arm64-2026-10-18-e35e66e`, SHA-256
  `637e881a050e340dba5f16b07b01a0b1f1d6c27d7f9a824f6e89b428570102d4`.
  `file` confirms a static ELF aarch64 executable. It has not been selected or
  installed; exact-digest Studio trust/update confirmation is pending.
- Current HEAD `c6c7e92` adds only this TODO checkpoint; the candidate contains
  the same Core source code and records its build revision in Go metadata.
- The running local Core is still the older binary recorded below and does not
  serve the secret catalog route. No Core container, database, Project or
  secret state was changed while preparing this candidate.
- Current-source Core owner gates already pass as recorded in the configuration
  submission/recovery checkpoint. Live secret catalog, rotation ACK, timeout
  and restart recovery remain unverified until this exact candidate is installed
  through Studio and exercised end to end.

## Configuration submission/recovery barrier — 2026-10-18

- `Submit` now serializes idempotency reservation and candidate-generation
  creation with snapshot recovery. Recovery can no longer observe the
  intermediate reservation-without-candidate state and fail a valid in-flight
  configuration apply. The lock is released before scheduling asynchronous
  apply; active snapshot validation/activation keeps the same barrier.
- Manual Core→Server child fixture now emits settings schema v2 and includes
  bounded operation diagnostics for candidate, active, open service-rollout,
  and traffic-rollout generations.
- Owner gates on this source: `make check` passed (Go build/tests, 96 Vitest
  files / 159 tests, typecheck, ESLint, amd64 Docker architecture lint);
  `make staticcheck-u1000`, `make test-race`, and `go vet ./...` also passed.
- Core's secret catalog/grant implementation was already present at this
  starting revision; this increment changes only the configuration recovery
  barrier and the manual fixture. Live macOS Studio/Server rotation
  acknowledgement and restart-recovery acceptance remain open.

## Local service-secret integration — 2026-10-18

- Core catalog and scoped grants, CLI OS-vault staging/rotation, and Studio's
  secure input, reference picker, Project-reference check, and exact
  replica digest/generation retirement guard are now implemented and pushed by
  their owners. Current source checkpoints: Core `b87dd1c`, CLI `f60ce65`,
  Studio `d6369bc`, Service SDK `110ac5e`, Server `2bc9f56`.
- This supersedes the earlier TODO above that said CLI vault staging and Studio
  host integration remained open. The remaining gap is live acceptance of
  multi-replica rotation acknowledgement, timeout/restart recovery, and the
  packaged Server Editor in macOS WebView; source checks do not close it.

## Live secret-catalog deployment mismatch — 2026-10-18

- Typed local runtime start created Core without updating its binary, then
  registered Echo and Server. The running Core container label is
  9189b7731207bd4434fcfbffa7d6a2083df1c1ceb79c1e9ae724c6199631b9b5;
  typed CLI service secret list receives HTTP 404 from the catalog route.
  Core source tests and OpenAPI include the route, so this is a deployed binary
  mismatch, not a source-contract pass for the running target.
- A Linux/arm64 binary rebuilt from current source checkpoint `b87dd1c` is at
  `/tmp/liapoldus-core-linux-arm64-2026-10-18-b87dd1c`, SHA-256
  `9aa6bce94f24239ca277af5008b1a094cb4909903d8d82ccb38b2ff1c66c127f`.
  The source change since the previous build was documentation-only, but this
  binary digest differs and has not been installed. Do not claim local secret
  catalog or rotation runtime acceptance until this exact candidate is
  authorized, installed through the typed Core binary update, and the
  list/create/rotate/apply/retire path is exercised.

## Operator service-secret reference catalog — 2026-10-17

- Core now stores only opaque reference ID, service ID, SDK purpose, label,
  lifecycle state and server creation time. SQLite schema v17 adds the
  service-scoped catalog and atomic audit writes; Management API exposes
  list/create/revoke metadata operations. Secret values, storage paths and
  grant handles are not accepted or returned by those operations.
- SDK secret grant issuance and redemption recheck active generation, reference
  owner, exact purpose and active state. Revocation between issue and redeem
  blocks access before the staged secret resolver runs. The local resolver
  accepts only direct regular files named by an opaque reference and rejects
  symlinks, traversal, oversized values and unsupported legacy file references.
- Focused Go packages and Management API tests pass. Contract publication,
  router/OpenAPI parity and contract digest checks pass. On 2026-10-17 the full
  Core owner gates passed: `make check`, `go vet ./...`,
  `go tool staticcheck -checks=U1000 ./...`, and Docker architecture lint. CLI
  vault staging and Studio host integration remain open in their owner
  repositories.

## Nomenclature migration regression — 2026-10-16

- Full `GOWORK=off go test ./...` initially caught
  `TestSchemaPreservesMigrationHistory`: migration edits had changed two SQL
  comments inside the immutable historical `SchemaSQL` baseline. Restored those
  original comment bytes instead of weakening the digest guard. Targeted storage
  tests and the full Core Go suite now pass. New service-table naming remains
  owned by its versioned migration; historical schema bytes stay unchanged.

## Studio configurator Core boundary — 2026-10-13

Scope: [`../studio/plan.md`](../studio/plan.md). Core owns only generic service
metadata, desired configuration generations, peer-link policy, typed operations
and explicit resource/restart state. Service-specific fields and UI descriptors
remain opaque; no Studio renderer or product behavior belongs in Core.

- [x] Local code includes Core-backed service manifest/schema/status reads and
  a safe service-replica registration boundary; prior workspace snapshot reports
  Core/SDK process smoke, but this inventory did not rerun those tests.
- [x] Core now matches CLI artifact ID/version/digest and control endpoint to the
  exact live authenticated SDK replica release/REST registration before accepting
  runtime registration; advertised peer endpoints are checked when present.
  The SDK release digest is explicitly treated as a replica claim, not proof of
  binary provenance. Live manifest identity/version is checked before the CLI
  registration reports success.
- [x] Management service status now returns instance/replica identity and the
  artifact ID/version/digest from the authenticated replica release alongside
  live identity/readiness metadata. Handler tests reject missing release
  provenance; Studio can fence manifest/schema reads against the selected
  Project artifact and detect replica/release changes. This binds reported
  metadata to Core's authenticated registration claim, not to measured binary
  bytes or cryptographic runtime attestation.
- [x] `GET /api/operations/{id}` now returns safe `replicaOutcomes` for durable
  configuration rollouts (replica ID, desired generation, pending/applied/failed
  and catalog error code only). The CLI operation client preserves the Core
  response, and Studio Output renders validated per-replica results; no replica
  endpoint, incarnation, settings payload or credentials enter the renderer.
- [x] `OperationReplicaOutcomeReader` расположен в отдельном domain-interface
  файле, сохраняя owner-правило одного объявления типа на файл. Полный Core
  Vitest suite прошёл 158/158 тестов; Go tests прошли после перестановки файла.
- [x] Metadata resolution now snapshots every currently leased replica's
  authenticated release claim in deterministic replica-ID order and fails
  closed when live version/digest claims are mixed. The Management status
  response carries the complete release set; handler tests reject inconsistent
  sets. SDK manifest/schema are fetched only after homogeneous release claims
  are established. This is a rollout consistency barrier, not binary attestation.
- [x] Manual Core→Server→forms-db smoke fixture now uses the current
  `server-http/v1` route contract (no route matcher alongside API path, matching
  methods, and typed request-body schema). `forms-db` accepts the additive opaque
  `parameters` member from Server's HTTP envelope while still decoding only the
  original body. `core/tests/integration/manual-core-server.test.ts` passed after
  reproducing and fixing the regression.
- [x] Revalidate the Core→Service SDK live metadata adapter after the local
  nomenclature changes. `TestCoreReadsLiveMetadataThroughServiceSDKRESTOverMTLS`
  uses the real SDK client and HTTP contract against an mTLS service endpoint;
  Core reads live manifest, configuration schema, identity and readiness from
  SDK-declared routes and pins the service URI identity.
- [x] Core child-process metadata acceptance now starts real Core, admits a
  Service replica over mTLS, and serves contract-fixture REST metadata from the
  authenticated endpoint. Management API manifest/schema/status results match
  the live responses, including an opaque `service-configuration-ui/v1`
  descriptor nested in the manifest. Focused child-process test passed
  2026-10-15. This proves Core process wiring and Core→SDK-client transport,
  not the production Service SDK handler implementation.
- [~] The full typed CLI→Core→Service SDK child-process path, including CLI
  registration and configuration apply, remains open. The direct
  CLI→Service REST path remains forbidden.
- [x] Relay the versioned configuration-UI descriptor unchanged inside the
  bounded service-owned manifest. Management API rejects metadata above 256 KiB
  and non-object JSON without interpreting widget semantics; handler tests cover
  opaque descriptor pass-through, over-limit documents and invalid root types.
- [ ] Define/verify typed resource publish and restart-required settings
  operations; preserve desired/effective revisions, approvals, recovery and
  replica applied-generation state.
- [x] Publish the implemented operator-only `GET/PUT /api/v2/settings` CAS
  boundary in Management OpenAPI, including desired/effective revisions and the
  restart-required state. Focused OpenAPI/router parity suite passes. This only
  closes contract discoverability; CLI typed update, Studio form, restart and
  recovery workflows remain open.
- [~] 2026-10-16 focused Core artifact-forwarding acceptance:
  `npm test -- --run integration/service-artifact-forwarding.test.ts` passes.
  It proves authorization, byte/metadata bounds, streaming, operation binding,
  idempotency enforcement and no filename forwarding for one service artifact
  action. It does not close the typed CLI→Core→SDK publish lifecycle, durable
  Project resource reference, operation recovery, restart-required settings or
  replica desired/effective-generation outcomes above.
- [~] Metadata size/root-type and opaque UI pass-through tests now pass. Add
  service ownership/authorization integration coverage and resource operation
  recovery/Core restart outcome tests; coordinate public contract changes with
  `cli/` and `service-sdk/` owners.
- [~] Local `PATH=<bundled-node>/bin:$PATH GOWORK=off GOFLAGS=-p=1
  LIAPOLDUS_DOCS_USE_WORKTREE=1 make check`, `go vet ./...`,
  `make staticcheck-u1000` and Docker architecture lint pass. The new live SDK
  metadata mTLS adapter test passes. Published-contract parity,
  authorization/ownership coverage, resource operation recovery and the Studio
  → CLI → Core → SDK child-process scenario remain open. Do not call the
  broader Core v3 production gates complete from this Studio-specific work.
- [x] Повторная локальная проверка 2026-10-14: с `GOTOOLCHAIN=go1.26.0`,
  `GOFLAGS=-p=1`, bundled Node и `LIAPOLDUS_DOCS_USE_WORKTREE=1` прошли
  `make build test typecheck lint` (96 Vitest-файлов прошли, 1 skipped,
  158 тестов passed). Изолированный `integration/manual-core-server.test.ts`
  прошёл отдельно за 76.81 с; в одном предыдущем полном запуске та же проверка
  дала отказ revoked-replica assertion, который не воспроизвёлся ни в
  изолированном повторе, ни в полном suite. Root cause пока не найден, поэтому
  это отмечено как наблюдавшаяся нестабильность, а не как исправленный дефект.
- [x] 2026-10-15: manual Core/Server/forms-db recovery poll теперь пережидает
  временный `ECONNREFUSED` при повторном поднятии HTTP listener наряду с
  bounded HTTP 503 retries. До исправления full `make check` падал на
  `/forms/list` сразу после restart; целевой `manual-core-server.test.ts`
  прошёл (1/1, 92.23 с), а полный Core gate затем прошёл: 96 Vitest-файлов,
  158 тестов, typecheck, lint и architecture lint.

## Завершённый breaking cutover

- [x] V2 bootstrap cutover: Core читает уже инициализированный SQLite state и
  открывает Management API; инициализация, запуск и lifecycle принадлежат
  standalone `liapoldus` CLI.
- [x] Settings Core хранятся в SQLite с CAS revisions, audit, desired/effective
  состоянием, pending restart и offline recovery к прежней revision.
- [x] SQL, storage schema/migrations и постоянные диагностические определения
  принадлежат Go-коду; независимый Go build/test и строгие lint-конфиги добавлены.
- [x] SDK/API contracts переведены на закреплённые независимые module versions;
  агрегатор документов использует pinned YAML source manifest.
- [x] Удалён static certificate resolver fallback. Membership для динамических
  replicas определяется authenticated registration и активными leases.
- [x] Legacy YAML migration и SQLite backup/restore удалены из Core; их
  реализация принадлежит standalone `liapoldus` CLI. Core не содержит
  migration entrypoint или offline database command.
- [ ] Завершить code-owned generation публичных schema/OpenAPI/error contracts
  и воспроизводимые проверки их публикации.
- [ ] Включить полный blocking quality gate на чистой ветке: Go, race, vet,
  staticcheck, architecture lint, TS tests/typecheck/lint и docs build. Go
  tests, Core Vitest (95 файлов, 156 тестов), TS strict typecheck, staticcheck
  U1000, typed ESLint (recommended + unsafe call/argument + async-safety rules)
  и architecture lint локально прошли. Отдельный audit unsafe
  assignment/member выявил 103 fixture JSON-boundary случая; добавлен object
  parser и закрыты 36 без подавлений. Остальные, golangci-lint и clean-branch
  CI прогон ещё не завершены.

## V3 — текущий этап

- [x] Удалить Core CLI радикально одним breaking slice: command dispatcher,
  `internal/presentation/cli`, `cmd/core-migrate` и Core-owned CLI tests.
- [ ] Согласовать и опубликовать Core Management API contract для canonical
  config bundle: commit SHA, bundle digest, schema version, target metadata,
  idempotency, CAS, plan/apply и operation observation.
- [ ] Реализовать cross-repository contract tests с `Liapoldus/cli`; Core не
  импортирует CLI и не открывает SQLite через CLI.
- [ ] Перевести local/remote/multi-target deployment на standalone CLI и
  GitHub CI. Core принимает только API requests; local process bootstrap и
  lifecycle выполняет CLI/adapter.
- [x] Удалить Core-owned backup/restore commands и file-maintenance API; runbook
  делегирует файловые операции универсальному CLI.
- Поддержать standalone Linux, Docker, Swarm и Kubernetes только после native
  smoke каждого профиля. Наличие deployment manifests само по себе не означает
  поддержку.
- Изменение конфигурации и управление rollout выполняются через Core API,
  вызываемый standalone CLI из immutable Git revision.
- Гарантировать seamless candidate rollout только на профилях с traffic
  controller, поддерживающим веса. Для standalone такую гарантию не заявлять.
- Core остаётся независимым от Ansible и не содержит управления его playbooks,
  установкой или процессами.

- Продолжить инфраструктуру Ansible в отдельном репозитории; продуктовые
  репозитории и Studio не зависят от него.
- Развивать Studio как отдельную среду проектов, файлов, Git и Studio services.
  Studio не имеет Core API adapter; target connections, deploy и Core
  lifecycle принадлежат standalone CLI/CI. Service configuration UI поставляет
  service; Studio встраивает только declarative surfaces.
- Укрепить cross-repository conformance для Service SDK REST+mTLS,
  включая host smoke с реальным service-owned applier.

## Проверки последнего прохода

- `PATH=<bundled-node>/bin:$PATH GOWORK=off GOFLAGS=-p=1 make test` — прошёл
  целиком: Go tests, полный
  Vitest, TypeScript typecheck и blocking ESLint.
- `GOWORK=off GOFLAGS=-p=1 go test ./...` — Core runtime/API и fixtures
  проходят после удаления встроенного CLI.
- Полный Core Vitest — 98 файлов прошли, 1 штатно skipped; 168/168 тестов
  прошли, включая ручной Core→Server→forms-db lifecycle и Admin Actions.
- `go vet ./...`, `make staticcheck-u1000`, `make arch-lint`, `git diff --check`
  — прошли.
  Архитектурный gate учитывает SQLite driver в settings adapter tests.
- `npm audit` в `tests/` — 0 vulnerabilities; Node type definitions закреплены,
  TypeScript typecheck добавлен в `make test`.
- `publish-contracts.mjs` и `verify-contract-bundle.mjs contracts/v1` прошли.
- VitePress aggregator build прошёл после исправления двух битых Core links.
  Не обновлять immutable source pins для незакоммиченных owner changes.
- Ручной Core→Server/forms-db прогон подтвердил restart, registration leases,
  settings rollback, service Admin query/delete, trust-root rotation и отказ
  от отозванной identity.
- Реальный Core→SDK→Domain→Runtime child-process gate теперь проверяет отказ
  Core активировать семантически неверные Domain grant и Runtime module-route
  generations (`activation_failed`), сохранение старой service readiness-state
  как pending/not-ready и восстановление после последующих валидных settings.
  Сценарий прошёл через TypeScript integration wrapper на bundled Node.
- Тот же real-process gate теперь синхронизирует Runtime SIGTERM с уже
  выполняющимся WASM host call: запись сначала подтверждается чтением из Domain,
  новые invocations отвергаются, принятый вызов завершается и Core видит
  deregistration. Runtime получает новый mTLS incarnation grant отдельной
  Core-owned Domain settings generation. Core `ServiceMetadata` callback также
  приведён к owner-типу `models.ServiceMetadata`, необходимому для сборки этого
  рабочего дерева.
- Последний полный локальный `make check` прошёл: Go build/tests, Vitest (95
  файлов passed, один skipped; 156 тестов passed), TypeScript typecheck/lint и
  architecture lint. До выполнения оставлены независимые gates из начала этого
  раздела: race/staticcheck/golangci-lint, чистая ветка, hosted CI и docs build.
  Сквозной Core→SDK→Domain→Runtime drain-тест также прошёл отдельно.
- После него acceptance fixture ускорен сборкой одного peer-client binary и
  ожиданием Core directory eligibility после service-local readiness. Это
  устранило корректный fail-closed `no_ready_peer` при eventual-consistent
  обновлении lease; guest drain loop остаётся bounded ниже пятисекундного ABI
  deadline. Два последовательных полных Core→SDK→Domain→Runtime process run
  прошли с `GOFLAGS=-race`. По ним добавлены закрытые Runtime host-call stage
  logs без payload/identity/endpoint или сырых transport errors. Текущие
  targeted architecture + integration tests прошли: 3 файла / 10 тестов.
- Server Go tests и полный Vitest suite — прошли; forms-db Vitest — 22 файла,
  35 tests passed, 6 skipped; отдельная Node contract проверка — 2/2.


## Candidate preflight and retention — 2026-10-17

- Core validates every service in a config bundle against its live SDK schema and service-owned `ValidateConfiguration` endpoint before creating any bundle operation. Single-service apply validates again before activation and during recovery. A service refusal removes the Core staging candidate; the canonical Project file remains available for correction/retry and active configuration/readiness is preserved.
- Real Core → SDK → Domain/Runtime integration passed, including invalid Runtime module route and Domain grant candidates, retained generation/readiness/data, valid recovery, and subsequent restarts. Full `make check` passed (96 Vitest files, 159 tests; one skipped), architecture lint and `go vet ./...` passed. SDK-pull fixture now explicitly stubs the added validation contract.
- Linux/arm64 Core candidate SHA-256 `8869c6f051324e1dac71211eca56daa540f290e0ceee4051eaace071bd4bfe9d` is built but not installed. Local smoke, commit/push and documentation pin update remain pending exact artifact confirmation and repository publication.

## Replica-scoped secret grants — 2026-10-18

- Core resolves mTLS certificates to the active SDK lease's full logical
  identity (`instanceId`, `replicaId`, `incarnationId`, `placementId`). A grant
  is bound to that identity and the exact verified certificate fingerprint;
  another replica or incarnation cannot redeem it. Certificates matching more
  than one active replica lease fail closed.
- Focused Go packages and real mTLS secret-grant/config-pull fixtures passed.
  `make check` passed: Go build/tests, 96 Vitest files passed, 1 skipped,
  159 tests passed, TypeScript typecheck, ESLint, and Docker `arch-lint`.
  `GOWORK=off GOFLAGS=-p=1 go vet ./...` and
  `GOWORK=off GOFLAGS=-p=1 make staticcheck-u1000` also passed.
- This hardening changes no public Service SDK contract and exposes no secret
  values or replica metadata through the Management API.
- Re-ran the real process-level mTLS grant fixture on 2026-10-18 with
  `GOWORK=off GOTOOLCHAIN=go1.26.0 GOFLAGS=-p=1 npm --prefix tests test -- integration/service-secret-grants.test.ts`;
  1/1 passed, covering active-generation scope, exact replica identity,
  one-use redemption and absence of secret/handle values from logs and errors.
- Rebuilt the local Core candidate from source commit `d7f82cd` for Linux/arm64:
  `/tmp/liapoldus-core-linux-arm64-2026-10-18-d7f82cd`, SHA-256
  `157fe9a08bd6c54888cef70abf173b13f34e56aa8666b1013bba0006b70fd1a7`.
  The previous `b87dd1c` candidate predates replica-scoped grant checks and is
  superseded. The new candidate has not been installed or run in the local
  target; the running Core container remains unchanged.

## Candidate rejection reporting — 2026-10-19

- A local config-bundle apply reached Server candidate validation, where the
  scoped secret grant was denied. Core kept generation 96 active and the
  Project candidate file remained available, but the CLI received a misleading
  revision-conflict response. The filtered Server log recorded only the safe
  `grantDenied` event; no credential or grant handle was read or copied.
- Core now uses a typed, detail-free candidate-validation failure. Bundle apply
  reports `bundle_invalid`/422, ordinary asynchronous config apply reports
  `service_config_invalid`, and schema transport failure reports
  `service_unavailable`; actual revision conflicts retain their conflict code.
  The traffic-rollout API uses the same safe validation/unavailable mapping.
  Private SDK validation details are not returned by these endpoints.
- `make check` passed (96 Vitest files, 159 tests; 1 skipped),
  `GOWORK=off GOFLAGS=-p=1 go vet ./...`, and `make staticcheck-u1000` passed.
  The real Core→SDK→Domain→Runtime integration test passed with the corrected
  `service_config_invalid` rejection code; the old active generation remained
  available after candidate refusal.
- Latest source commit: `43b7acc35bd2c640b08b769a335b127581190cdd`.
  Clean Linux/arm64 Core candidate:
  `/tmp/liapoldus-core-43b7acc-linux-arm64`, SHA-256
  `5ccf6e176dd9784b4deccb5c033653646b253f66a716ebf0032b27c9c2ee7af8`.
  Build metadata reports `vcs.modified=false`. This digest is not installed;
  local apply/secret smoke remains pending exact approval for this build.

## Local Bearer rotation smoke — 2026-10-19

- Через текущий ранее одобренный Core в `Server Echo Studio` / `local` прошёл
  реальный поток Studio → CLI → Core → Service SDK → Server: тестовая Bearer
  ссылка создана и ротирована, кандидат с отдельным auth-route получил
  подтверждение в Studio, Core применил generation 98, а Server подтвердил
  точный digest. Запрос с новым токеном вернул 200; без токена и с неверным
  значением — 401.
- После удаления тестового route Core применил очищенный Project generation 99;
  Server подтвердил его. Все временные ссылки отозваны только после ACK и
  удаления ссылки из Project. Typed `service secret list` показывает 0 active,
  7 revoked, 0 pending registrations и 0 pending rotations. Значение секрета
  не записывалось в Project, Core SQLite, логи или события.
- Это smoke на уже запущенном Core `df625da7…` и временно разрешённом Server
  1.1.13, а не приёмка нового Core candidate `5ccf6e17…`: candidate остаётся
  неустановленным до точного digest approval. Локальная проверка завершена с
  Project pin и работающим Server 1.1.12; временная запись Server 1.1.13
  удалена из локального каталога.
