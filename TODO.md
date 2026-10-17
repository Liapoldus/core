# Core v3 — production readiness

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
