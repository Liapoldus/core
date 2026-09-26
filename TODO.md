# TODO — Gateway core

Единый архитектурный roadmap и очередность работ:
[Перепроектирование Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Здесь перечислены только незавершённые задачи core.

## Актуальный прогресс — 26.09.2026

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
  получает reader с bootstrap artifacts path. Archive reader проверяет digest
  исходного `.tar.gz` и строит frontend manifest из staged roots.
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
- `OperationStore` подключён через domain port, application service и SQLite
  adapter. Restart operation и `GET /api/operations/{operationId}` используют
  durable store; TS integration проверяет закрытие/повторное открытие SQLite,
  неизвестный ID (`404`) и отсутствие opaque result/problem payloads. Group
  publish/rollback используют durable operation lifecycle; generic restart
  transitions, recovery и audit для остальных mutations остаются незавершёнными.
- Добавлен SQLite foundation для group release coordination: durable journal,
  CAS проверки expected current revision, reservation для idempotency key с
  digest-only key storage, одинаковый запрос возвращает исходный operation,
  повтор ключа с другим request digest отклоняется, а commit атомарно вставляет
  revision, переключает `previous/current`, обновляет operation и append-only
  audit. TS integration проверяет duplicate/conflict, commit и reopen SQLite.
  Management API принимает multipart Caddyfile и необязательный `.tar.gz`;
  Caddyfile адаптируется до reservation, archive извлекается в immutable
  staging с digest и ограничениями пути/размера/количества/ratio. Небезопасная
  запись проверяется TS integration и возвращает `422 artifact_invalid` до
  activation. Публикация синхронизирует snapshot через Caddy activator; rollback
  активирует previous revision и после успеха атомарно меняет current/previous.
  TS E2E проверяет safe archive frontend digest/manifest, traversal rejection,
  rollback, idempotent retry и stale-current conflict. Golden vectors дополнительно
  исполняют gzip checksum, duplicate/case-fold collision и NFC normalization.
  Не закрыты все numeric archive limits, сериализация concurrent same-CAS
  reservations и production startup crash-recovery.
- Текущий focused group suite: archive, rollback, release-store и Management API
  tests проходят. После объединения параллельных изменений прошёл полный
  `make check` (Go build, 44 TS-файла / 73 теста и Docker arch-lint без
  warnings), `go vet ./...`, Linux/macOS ARM64 builds и Gateway Docker smoke.
  Это не закрывает незавершённые production lifecycle/conformance пункты ниже.
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
- Local plugin launch использует normalized `plugin_launch_settings` только
  для абсолютного пути бинарника; приложение не получает argv, environment или
  локальные config-файлы. Gateway передаёт заранее открытый loopback listener
  как inherited FD, посылает служебный typed `Bootstrap`, затем сам выполняет
  `ConfigApply` с SQLite settings revision до health/readiness. Config остаётся
  in-memory в процессе plugin; Caddy dispatch snapshot не содержит settings и
  только проверяет manifest/health. Child-process TS E2E покрывает FD,
  application-env isolation, push перед вызовом, crash/restart, scoped
  ConfigApply secret redemption и reap при остановке Gateway. Gateway разрешает
  generic external `file:` refs только для ConfigApply, ограничивает каждый
  файл 64 KiB, заменяет путь opaque ID и выдаёт одноразовый grant для instance
  и settings revision; исходные байты удерживаются только в памяти runtime для
  повторного ConfigApply после рестарта и очищаются при остановке. Ротация файла
  применяется только новой settings revision. Нужно добавить contract-level
  тесты на очистку буферов и отказ размера/типа файла. External Caddy намеренно
  сообщает data plane `not-ready` при сохранённых plugin instances: private
  immutable dispatch sync ещё не реализован.
- Bootstrap-проверки оставлены и обновлены: TS integration проверяет загрузку
  относительных путей, запрет прежнего route DSL и отказ удалённому bind без
  client CA. `bearerVerifier` удалён из core fixtures/tests.
- Убраны недействующие CLI-конфигурационные и `site` команды; CLI оставлен для
  `serve` и `access bootstrap`. Статический CLI contract сокращён до реально
  используемых слов и настроек bootstrap key.
- Undocumented `GET /api/operations` удалён. `GET
  /api/operations/{operationId}` ранее читал volatile in-memory map; теперь
  использует durable SQLite store. Domain-модель `Operation` содержит только
  metadata; opaque result/problem payloads не сохраняются и не выдаются.
- Удалены оставшиеся не маршрутизируемые `/api/sites` publish/rollback
  handlers, их volatile idempotency/revision-conflict обвязка и domain error;
  добавлен TS architecture gate, не допускающий возврат старого registry API.
- Удаление старых metrics/tracing exporters не закрывает целевую observability:
  документационные требования к Gateway/Caddy/plugin readiness, access и
  application logs, metrics, traces и общей redaction policy остаются в v1
  roadmap и должны быть реализованы заново на новых runtime adapters.
- Ранее после cleanup проходил Vitest 25 files / 43 tests; этот результат
  исторический и не заменяет актуальный полный `make check`. Ни один из этих
  срезов не означает готовность Gateway v1.

## Документальный контракт и тестовый фундамент

- [x] Удалить недоступные legacy config/site/release handlers и связанные с
  ними неиспользуемые модели/контрактные поля. Целевые group release, plugin,
  TLS и Caddy Admin surfaces остаются незавершёнными задачами ниже.
- [ ] Продолжать отдельные TypeScript unit/integration/E2E suites под tests для
  bootstrap rejection, SQLite, group multipart, native Caddyfile adaptation,
  external-Caddy lifecycle, Caddy Admin checkpoint/drift/reconcile, direct
  Caddy-to-plugin dispatch и crash recovery. Уже есть исполняемые tests для
  Management auth/bootstrap rejection, group publish/archive/rollback,
  Caddy-L4 TCP/UDP, HTTP Stream/WebSocket/SSE и external-Caddy restart/status.
- [ ] Добавить TS red/green coverage до расширения целевого data/control-plane:
  multipart group publish/rollback, Caddy adapt/load and atomic snapshot,
  external Caddy process, plugin replica readiness/DispatchApply, Admin API
  checkpoint/reconcile и crash recovery. Cookie boundary red/green coverage
  имеется для изолированного Caddy `call` slice; его production composition
  остаётся незавершённой. Legacy runtime suites удалены, а не объявлены
  эквивалентом этих ещё не написанных проверок.

## Bootstrap, persistence и Management API

- [ ] Завершить Management API semantics и storage для операций, audit,
  idempotency и Caddy checkpoints; реализованы Bearer service-key verifier,
  group/release operations и часть durable audit, но не все mutation families.
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
- [x] External-Caddy runtime запускается из `serve` с закрытым локальным
  control socket; focused E2E проверяет startup/restart/status. Embedded/external
  parity, module/build identity и полная deployment conformance остаются открыты.
- [ ] Зафиксировать Caddy/xcaddy/module versions, build identity/module
  manifest, supply-chain verification и общий parity matrix.
- [x] Caddy-L4 TCP/UDP plugin dispatch прошёл focused E2E; общий parity gate
  embedded/external variants на macOS/Linux остаётся обязательным. Провал
  блокирует v1, Go net/gnet fallback не разрешать.
- [ ] Довести native Caddyfile validation/adaptation, system group и стабильную
  composition application groups до production startup/recovery без Gateway
  route DSL.
- [ ] Довести multipart group release до полного v1: один безопасный `.tar.gz`,
  frontend roots, архивные limits/normalization/digests, plugin binding
  validation, immutable staging, full-snapshot activation и rollback current/
  previous. Текущий вертикальный срез принимает multipart, проверяет Caddy
  adaptation, CAS/idempotency в SQLite, сохраняет revision/operation и активирует
  composed snapshot через Caddy activator. Archive staging/manifest, rollback API
  и SQLite pointer swap реализованы и проверяются focused E2E. Не доказаны unique
  in-flight CAS reservation и crash recovery между activation/commit.
- [x] TS integration фиксирует multipart
  `POST /api/groups/{id}/releases`, durable `OperationReference`, invalid
  Caddyfile, stale current revision, idempotent retry/conflicting key и reopen
  SQLite; archive/rollback покрыты отдельными focused tests. Production traffic
  activation остаётся за пределами API-fixture activator.
- [x] TS integration red-tests проверяют traversal rejection `422
  artifact_invalid` до activation и positive safe `.tar.gz` staging, archive
  digest и frontend manifest digest.
- [x] Добавить и исполнять archive vectors для gzip checksum integrity,
  duplicate path, case-fold collision и NFC normalization. NFC имена принимаются
  после нормализации; дубли/коллизии и повреждённый gzip дают `artifact_invalid`
  до изменения active revision.
- [ ] Добавить archive vectors для path depth/length, compressed/uncompressed
  byte, ratio и entry limits.
- [x] TS integration проверяет `POST /api/groups/{id}/rollback`: durable
  operation, previous activation, CAS conflict, idempotent retry и атомарный swap
  current/previous. Нет отдельного `current`/`previous` endpoint в контракте;
  pointer metadata читается через Group API.
- [x] TS integration red-test `operation-persistence.test.ts` требует, чтобы
  Operation, созданная существующим restart endpoint, переживала закрытие и
  повторное открытие SQLite, а неизвестный ID давал OpenAPI Problem 404; SQLite
  operation store и metadata-only API response реализованы. Persisted arbitrary
  `result` запрещён.
- [ ] Реализовать startup reconciliation незавершённых операций; crash между
  artifact/SQLite commit/Caddy activation не должен создавать смешанный runtime.
- [ ] Реализовать полный Admin API pass-through к loopback/local IPC; checkpoint
  до каждой mutation, drift detection, group publish block, explicit checkpoint
  restore и full-composition reconcile с If-Match.
- [ ] Не обещать обратную генерацию Caddyfile из произвольного native Caddy JSON.

## Plugins, TLS и Constructor boundary

- Локальный запуск передаёт inherited listener и не использует argv,
  environment или application config file плагина; Gateway отправляет настройки
  конкретной revision через typed Bootstrap/ConfigApply до проверки readiness.
  Эту границу покрывает child-process TS E2E, но полный release gate ещё не
  пройден. Остаются: remote mTLS identity/revocation и endpoint sets;
  DispatchApply readiness barrier; immutable external-Caddy dispatch sync;
  management CRUD для launch settings; configurable restart/resource/grant
  policy. External Caddy с настроенными plugin instances остаётся fenced и не
  готовым к parity.
- HTTP `Stream` contract/handler фиксирует route concurrency, но отсутствуют
  configurable per-instance shared concurrency, idle-timeout и max-duration
  controls. Добавить их отдельным protocol/runtime contract + TS conformance;
  unary timeout не использовать для долгоживущих streams.

- Caddy handler валидирует capability invocation modes `call`, `http-stream`,
  `websocket`, `sse`, `tcp` и `udp`; mode capability tests есть. HTTP Stream,
  WebSocket и SSE focused E2E проходят 3/3, TCP/UDP — отдельный Caddy-L4 E2E.
  Route concurrency guard уже есть, но configurable per-instance shared limit,
  idle-timeout и max-duration остаются TODO выше. Local `call` dispatch через
  embedded Caddy подключён к supervised child; dispatch generations, local TLS
  settings, config-scoped grants and remote-replica fan-out ещё не подключены.
- [ ] Расширить `tests/fixtures/serve-plugin-child` lifecycle harness до
  child-process smoke с реальными сборками captcha, forms-db и identity после
  полного serve wiring. Текущие тесты подтверждают generic fixture capability
  и supervision, но не являются интеграционной приёмкой этих трёх plugins.
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

- Staticcheck закреплён как Go tool dependency: Staticcheck 2026.1 (`v0.7.0`),
  запускается через `make staticcheck-u1000` с Go 1.26.0. Старый бинарник
  2025.1.1 (`v0.6.1`), собранный с Go 1.24.1, несовместим с модулем core и
  не должен использоваться.
- Vitest/E2E требует Node.js >=22: `package.json` и `tests/package.json`
  декларируют `engines`. WebSocket E2E использует встроенный global `WebSocket`
  и проверяет реальное соединение с Gateway; не заменять его заглушкой. CI
  закреплён на Node 24. Локальный baseline этой сессии — Node 26.3.0
  (`typeof WebSocket === "function"`).
- [ ] Для каждого increment сначала отдельные красные TypeScript tests, затем
  реализация; Go test files в production packages не добавлять.
- [ ] Для milestones запускать make check, go vet ./..., go build ./...,
  macOS/Linux builds и подходящие Docker/Caddy variant smoke suites.
- [ ] Добавить immutable dispatch generations и external-Caddy private Admin
  API/IPC synchronization; failure сохраняет прежний runtime generation.
- [ ] Подключить capability→modes descriptor из `pluginprotocol` Manifest к
  group publish pre-activation validation; текущий Caddy handler знает mode
  registry, но management/serve composition не валидирует весь published group
  against live instance Manifest до activation.
- [ ] Расширять исполняемые golden-vector conformance: сейчас семь vectors
  реально исполняются (bootstrap rejection, Management authentication и пять
  archive cases); прочие vectors пока проверяются только структурно либо
  требуют отдельного production slice.
- [ ] Исправить release workflow: он требует минимум 9 файлов в
  `contracts/v1`, хотя manifest перечисляет 7 payload-файлов и каталог содержит
  8 файлов. Проверять соответствие package contents manifest, а не фиксированный
  порог.
- [ ] Завершить прямой Caddy gRPC `Call`/`Stream` production composition:
  HTTP bidi, WebSocket, SSE и L4 focused handler/E2E slices уже есть, но full
  serve generation lifecycle, cancellation/backpressure/limits и deployment
  parity остаются не закрыты end-to-end.
- [ ] Не объявлять v1 готовым без полного cross-variant HTTP/TLS/ACME/L4/plugin/
  SQLite/recovery/security/admin-proxy conformance.
