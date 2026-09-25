# TODO — Gateway core

Единый архитектурный roadmap и очередность работ:
[Перепроектирование Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Здесь перечислены только незавершённые задачи core.

## Актуальный прогресс — 25.09.2026

- Production `serve` использует bootstrap + SQLite, а Management API защищён
  SQLite-backed Bearer verifier. Пути state/artifacts разрешаются относительно
  `gateway.yaml`; пустой Gateway запускается без public data plane.
- `/api/groups` и `/api/groups/{id}` читают SQLite. `POST /api/groups` создаёт
  application group, проверяет опубликованные ID/idempotency constraints,
  возвращает `201`, `400 invalid_request` или `409 group_already_exists`.
- `GET /api/groups/{id}/releases` выдаёт metadata-only summaries из SQLite с
  cursor pagination; response не раскрывает Caddyfile или пути артефактов.
- `GET /api/groups/{id}/releases/{revisionId}` проверяет Caddyfile digest и
  читает файл только внутри immutable artifacts root; production `serve`
  получает reader с bootstrap artifacts path. Детализация frontend manifest для
  release с archive остаётся незавершённой до реализации publish.
- Production `serve` теперь подключает append-only SQLite audit store;
  успешный `group.create` и его audit row коммитятся в одной SQLite transaction;
  если audit insert падает, API возвращает 503 и группа не создаётся. Ошибочные
  попытки `group.create` записываются отдельно до ответа; если failed-attempt
  audit append не удаётся, исходный problem response заменяется на
  `503 audit_unavailable`. `/api/audit` возвращает newest-first cursor
  pages, prune-ит истёкшие записи и переживает restart. Проверены paging,
  invalid cursor/limit, retention, restart и атомарный rollback при audit error.
  Остальные mutations, durable-operation transitions и общая политика ошибок
  audit store остаются незавершёнными.
- По разрешённому cleanup удалены старый `internal/infrastructure/network`,
  CompiledGraph/config DSL compiler и renderer, site/release registry и snapshot
  stores, их CLI/account store, GeoIP/MMDB runtime и telemetry exporters.
  Дополнительно удалены неиспользуемые domain-модели `Action`, `Capability`,
  `ClientAuth`, `Revision`, `RegistryLockConflict`, пустой `ManagementStore`,
  незадействованный filesystem `FilesystemAuditStore` и неисполняемая fixture
  `plugin-grant`. SQLite-backed audit storage теперь сохраняет и листает
  события, но нужно расширить запись на все state mutations и обеспечить
  атомарность/ошибки audit вместе с durable operations.
  Удалены неиспользуемые route/proxy/WAF/Geo/telemetry domain модели и старые
  route/policy/resolver ports. Management contract больше не публикует config,
  site, listener/upstream или metrics endpoints. Удалены привязанные к ним
  красные legacy suites и фикстуры; групповой SQLite,
  минимальный bootstrap, Caddy runtime и plugin fixtures сохранены.
- Bootstrap-проверки оставлены и обновлены: TS integration проверяет загрузку
  относительных путей, запрет прежнего route DSL и отказ удалённому bind без
  client CA. `bearerVerifier` удалён из core fixtures/tests.
- Убраны недействующие CLI-конфигурационные и `site` команды; CLI оставлен для
  `serve` и `access bootstrap`. Статический CLI contract сокращён до реально
  используемых слов и настроек bootstrap key.
- Удалён undocumented `GET /api/operations`, который выдавал только volatile
  in-memory список и отсутствовал в OpenAPI; сохранён документированный
  `GET /api/operations/{operationId}`. Удалена неиспользуемая domain-модель
  `Operation`; runtime API-модель остаётся нужна restart/poll flow.
- Удалены оставшиеся не маршрутизируемые `/api/sites` publish/rollback
  handlers, их volatile idempotency/revision-conflict обвязка и domain error;
  добавлен TS architecture gate, не допускающий возврат старого registry API.
- Удаление старых metrics/tracing exporters не закрывает целевую observability:
  документационные требования к Gateway/Caddy/plugin readiness, access и
  application logs, metrics, traces и общей redaction policy остаются в v1
  roadmap и должны быть реализованы заново на новых runtime adapters.
- Последняя проверка после cleanup: Vitest 25 files / 43 tests, architecture
  lint без предупреждений, `go vet ./...`, `go build ./...` и
  `git diff --check` прошли. Это не означает готовность Gateway v1.

## Документальный контракт и тестовый фундамент

- [x] Удалить недоступные legacy config/site/release handlers и связанные с
  ними неиспользуемые модели/контрактные поля. Целевые group release, plugin,
  TLS и Caddy Admin surfaces остаются незавершёнными задачами ниже.
- [ ] Создать отдельные TypeScript unit/integration/E2E suites под tests для
  bootstrap rejection, SQLite, group multipart, native Caddyfile adaptation,
  Caddy Admin checkpoint/drift/reconcile, direct Caddy-to-plugin dispatch и
  recovery.
- [ ] Добавить TS red/green coverage до расширения целевого data/control-plane:
  multipart group publish/rollback, Caddy adapt/load and atomic snapshot,
  external Caddy process, plugin replica readiness/DispatchApply, Admin API
  checkpoint/reconcile и crash recovery. Cookie boundary red/green coverage
  имеется для изолированного Caddy `call` slice; его production composition
  остаётся незавершённой. Legacy runtime suites удалены, а не объявлены
  эквивалентом этих ещё не написанных проверок.

## Bootstrap, persistence и Management API

- [ ] Завершить Management API semantics и storage для операций, audit,
  idempotency и Caddy checkpoints; текущий bootstrap slice покрывает только
  service-key verifier и группы.
- [ ] Расширить SQLite audit events на все management mutations и durable
  operation transitions; сейчас `group.create` success атомарен с audit row,
  а failed attempts записываются до ответа. Обеспечить такую же транзакционную
  согласованность и явную обработку audit errors для остальных mutations.
- [ ] Реализовать строго минимальный gateway.yaml: state SQLite path, immutable
  artifacts root, Management bind/TLS/trust и embedded/external Caddy variant.
- [ ] Разработать SQLite migrations/repositories для groups/revisions/current/
  previous, plugin instances, service-key verifier metadata, operations,
  idempotency, audit, checkpoints и retention.
- [ ] Закрепить foreign keys, WAL, transaction isolation, локальный storage,
  backup/restore и crash recovery; SQLite хранит durable metadata, а
  Caddyfile/plugin settings revisions и большие artifacts остаются immutable
  files. На старте строить immutable in-memory RuntimeSnapshot; request path не
  читает SQLite/config files. Запретить dangling revision/artifact refs.
- [ ] Реализовать Management API security: private-network web backend доступ
  по mTLS + per-Gateway Bearer `platform-admin`; loopback/SSH-forwarded access
  по Bearer с TLS server verification. Gateway не реализует Constructor users
  или RBAC. Обеспечить one-time token reveal/rotation/revocation и redaction.
- [ ] Обновить CLI: bootstrap status/migrations, service-key lifecycle,
  group inspect/rollback, Caddy build identity, drift/checkpoint/restore и
  reconcile.

## Caddy variants, groups и Admin API

- [ ] Собрать embedded Caddy и запускать compatible external custom Caddy binary
  как обязательные v1 variants; stock Caddy недопустим.
- [ ] Зафиксировать Caddy/xcaddy/module versions, build identity/module
  manifest, supply-chain verification и общий parity matrix.
- [ ] Встроить обязательный Caddy-L4 в оба variants и пройти TCP/UDP conformance
  для macOS/Linux; провал блокирует v1, Go net/gnet fallback не разрешать.
- [ ] Реализовать native Caddyfile validation/adaptation, system group и
  стабильную composition application groups без Gateway route DSL.
- [ ] Реализовать один multipart group release: metadata CAS/idempotency,
  Caddyfile fragment, optional single safe tar.gz, frontends roots, digest,
  staging limits, durable artifact commit, full snapshot prepare/activate и
  rollback current/previous.
- [ ] TS integration red-test фиксирует корректный multipart
  `POST /api/groups/{id}/releases` и ожидает durable `OperationReference`.
  Endpoint пока намеренно не реализован: сначала нужны durable operation и
  idempotency storage, immutable safe artifact staging, Caddy candidate
  adaptation/full-snapshot activation, plugin binding validation и согласованный
  journal/recovery для Caddy runtime и revision pointers. Не выпускать
  промежуточный handler, который принимает upload без полной activation-семантики.
- [ ] TS integration red-test `operation-persistence.test.ts` требует, чтобы
  Operation, созданная существующим restart endpoint, переживала закрытие и
  повторное открытие SQLite, а неизвестный ID давал OpenAPI Problem 404. До
  реализации API хранит operations только в памяти; persisted arbitrary
  `result` запрещён, безопасный metadata-only ответ достаточен по OpenAPI.
- [ ] Реализовать startup reconciliation незавершённых операций; crash между
  artifact/SQLite commit/Caddy activation не должен создавать смешанный runtime.
- [ ] Реализовать полный Admin API pass-through к loopback/local IPC; checkpoint
  до каждой mutation, drift detection, group publish block, explicit checkpoint
  restore и full-composition reconcile с If-Match.
- [ ] Не обещать обратную генерацию Caddyfile из произвольного native Caddy JSON.

## Plugins, TLS и Constructor boundary

- Имеется ограниченный, пока не подключённый к `serve` Caddy `call` slice:
  `StartCaddyfileWithPlugins`, per-config handshake и
  `liapoldus_plugin <instance> <capability> call`. В этом slice реализованы
  protocol-owned inbound cookie allow-list и атомарные typed cookie response
  actions; production startup composition, snapshot generations и
  remote-replica fan-out не подключены. Сохранить этот код при подключении
  composition root и расширять по red TS тестам.
- [ ] Оставить core plugin-agnostic: CRUD generic instances/capability
  manifests/modes, local supervision и remote explicit per-replica endpoint sets без
  конкретных plugin names; режим задаётся per instance, mixed deployments
  разрешены; Caddy handler напрямую вызывает объявленную capability.
- [ ] Развести Gateway control gRPC client и Caddy data-plane connection pool:
  контроль выполняет Manifest/Config/health/lifecycle, Caddy напрямую вызывает
  только Call/Stream. Для remote выдать раздельные scoped workload identities;
  activation generation ждать readiness обоих каналов, передавать Caddy только
  immutable endpoint/capability/limit и credential references, не handles или
  plaintext ключи.
- [ ] Включить remote mTLS с уникальной externally-issued identity на каждую
  workload replica и привязкой к logical instance. Проверять protocol handshake
  на каждом новом connection и agreement по protocol version,
  release/Manifest/settings digests у всех Ready replicas; Management/plugin
  trust roots разделить, rotation/revocation
  выполнять без downgrade. `DispatchApply` уже реализован в `pluginprotocol` v1;
  Gateway обязан применить candidate generation к каждой Ready replica и
  получить/проверить индивидуальный acknowledgement до activation. Решение v1
  зафиксировано в [canonical plugin deployment](https://liapoldus.github.io/gateway/architecture/plugin-deployment):
  Management API хранит явный desired set stable endpoint-ов, каждый endpoint
  адресует одну replica; core не вызывает Docker/Kubernetes API и не принимает
  общий load-balanced Service как membership. Изменение размера/адресов набора
  требует явного Management API update. Реализовать endpoint storage, readiness,
  `DispatchApply` barrier и atomic dispatch/Caddy generation.
- [ ] Реализовать bounded local restart/reconnect и remote Service reconnect;
  не повторять unary Call с неопределённым исходом, закрывать in-flight Streams,
  а недоступность одного plugin отражать только на связанных bindings.
- [ ] Сохранить Gateway-owned scoped GrantBroker и отсутствие direct
  plugin-to-plugin traffic. Пользовательский capability traffic идёт напрямую
  от Caddy handler к plugin, не через Gateway Management API или application
  dispatcher; GrantBroker обслуживает только отдельное redemption.
- [ ] Оставить Caddy/CertMagic единственным ACME owner; добавить readiness по
  домену и domain renew/revoke adapter/API без второго ACME state machine.
- [ ] Поддержать Constructor только через Management REST API; Caddy Admin port
  не публиковать, credentials не возвращать React renderer.

## Обязательные gates

- [ ] Для каждого increment сначала отдельные красные TypeScript tests, затем
  реализация; Go test files в production packages не добавлять.
- [ ] Для milestones запускать make check, go vet ./..., go build ./...,
  macOS/Linux builds и подходящие Docker/Caddy variant smoke suites.
- [ ] Добавить immutable dispatch generations и external-Caddy private Admin
  API/IPC synchronization; failure сохраняет прежний runtime generation.
- [ ] Проверять все Caddyfile binding `instance/capability/mode` по
  capability→modes descriptor из `pluginprotocol` Manifest до активации
  revision. Закоммиченный Caddy adapter сейчас проверяет только `call`; эту
  проверку нужно перенести/подключить в будущую публикацию и активацию group
  revision, а остальные invocation modes пока не поддержаны.
- [ ] Сделать golden vectors исполняемыми conformance-сценариями: текущая
  architecture-проверка подтверждает только структуру и checksum списка, но не
  поведение Gateway.
- [ ] Исправить release workflow: он требует минимум 9 файлов в
  `contracts/v1`, хотя manifest перечисляет 7 payload-файлов и каталог содержит
  8 файлов. Проверять соответствие package contents manifest, а не фиксированный
  порог.
- [ ] Подключить прямой Caddy gRPC `Call`/`Stream` boundary, включая HTTP bidi,
  WebSocket, SSE и L4; application/control plane не буферизует пользовательский
  body.
- [ ] Не объявлять v1 готовым без полного cross-variant HTTP/TLS/ACME/L4/plugin/
  SQLite/recovery/security/admin-proxy conformance.
