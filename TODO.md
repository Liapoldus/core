# TODO — Gateway core

Единый архитектурный roadmap и очередность работ:
[Перепроектирование Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Здесь перечислены только незавершённые задачи core.

## Актуальный прогресс — 27.09.2026

- Начато структурирование `internal/presentation`: D0 сделал семь source-text
  guards path-agnostic через рекурсивный просмотр Go-пакетов `api/` и `cli/`;
  негативные мутации в будущих вложенных путях подтвердили, что assertions
  продолжают ловить соответствующие нарушения. D1 добавил явные go-arch-lint
  components для `api/handlers`, `cli/caddyruntime` и `cli/bootstrap`; временные
  запрещённые импорты Caddy и `pluginprotocol` в каждом из трёх подпакетов
  отклонены линтером. Presentation baseline `GOTOOLCHAIN=go1.26.0 go tool
  staticcheck -checks=U1000 ./internal/presentation/...` — 0 orphan diagnostics.
  Следующий шаг: characterization и переносы по утверждённой
  последовательности; форму `api.Server` не менять.

- `POST /api/access/service-keys` выпускает service key с именем длиной 1–80
  символов: raw token возвращается только в ответе `201`, в SQLite сохраняются
  verifier и метаданные, а новая credential сразу проходит Bearer-аутентификацию
  `/api/status`. Создание ключа и успешная audit-запись — одна SQLite transaction;
  TS child-process E2E проверяет отсутствие token в DB/WAL/SHM и последующих
  Management responses, а также что отказ audit не выдаёт credential и не
  оставляет запись ключа. Key list/rotate/revoke остаются незавершёнными.

- `GET /api/plugins/{pluginId}` возвращает тот же redacted inventory object,
  что и список, не раскрывая settings, endpoint, launch path или grants;
  неизвестный instance отвечает canonical `plugin_not_found`. TypeScript
  child-process integration проверяет detail, redaction и 404; сохранённый
  `/api/plugins/admin-surfaces` проверяется тем же тестом после явного
  разрешения route precedence. Create/update/delete plugin instance пока не
  реализованы.

- Production `serve` использует bootstrap + SQLite, а Management API защищён
  SQLite-backed Bearer verifier. Пути state/artifacts разрешаются относительно
  `gateway.yaml`; пустой Gateway запускается без public data plane.
- Первая system revision теперь принимается из management-only bootstrap state:
  lazy Caddy activator проверяет candidate без открытия public listener, затем
  запускает data plane при activation и переводит readiness в `ready`. TS E2E
  публикует первую system revision через API и запрашивает ответ с реального
  embedded Caddy listener. External variant теперь передаёт `PluginDispatchBinding`
  в custom Caddy config: настоящий Caddy binary загружает тот же Liapoldus
  dispatch app и вызывает plugin напрямую. TS E2E проверяет plugin `Call` и то,
  что отвергнутый Caddy candidate сохраняет прежний обслуживающий snapshot.
  Полная cross-variant conformance остаётся открытой.
- При старте с существующей revision `serve` теперь сначала разрешает pending
  release reservations в SQLite и только после успешного recovery активирует
  Caddy через lazy runtime. TS E2E инъектирует ошибку durable transition и
  подтверждает `recovery-required` при закрытом public listener. Новый TS E2E
  запускает реальный Gateway `serve` с собранным custom Caddy, содержащим
  Liapoldus HTTP/L4 modules. Только тестовая Unix Admin proxy оборачивает Caddy:
  она пропускает `/load`, ждёт успешный ответ настоящего Admin API и удерживает
  его до SIGKILL Gateway. E2E дополнительно подтверждает candidate через публичный
  listener и `pending` в SQLite до kill; после restart Caddy отдаёт старую
  композицию, operation/journal завершены как failed, staging удалён. Этот
  сценарий закрывает указанную границу process crash, но не весь crash recovery
  и не общий external-Caddy conformance gate.
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
- Cookie-policy PUT теперь поддерживает и supervised external Caddy: Gateway
  строит полный candidate Caddy config с обновлённым Liapoldus plugin dispatch
  app, применяет его через закрытый Admin Unix socket и только после успешного
  `/load` фиксирует SQLite CAS/audit. При ошибке CAS предыдущий dispatch snapshot
  отправляется обратно. Новый red/green TS E2E запускает реальный custom Caddy
  child process, меняет allow-list через Management API и после ответа проверяет
  входящие cookie на публичном запросе. Его fault-injection продолжение
  заставляет SQLite отклонить CAS после принятого Caddy `/load`: API возвращает
  `503`, ETag/DB policy остаются прежними, а следующий public request видит
  восстановленный allow-list. Полный `make check` после обоих external-Caddy
  сценариев прошёл: 60 файлов / 109 тестов, Go build и Docker arch-lint;
  `go vet ./...`, Staticcheck U1000,
  Linux amd64/macOS arm64 builds и `git diff --check` также прошли. Полная
  cross-variant parity, crash recovery и rollback-failure fencing остаются
  отдельными gates.
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
  исполняют gzip checksum, duplicate/case-fold collision, NFC normalization,
  path depth/byte length, entry-count, compression-ratio и compressed/uncompressed
  byte limits. Byte-limit vectors уменьшают порог только внутри fixture, не
  создавая сотни MiB; превышение uncompressed entry size возвращает
  `artifact_too_large`. Отдельный concurrent HTTP/SQLite E2E теперь подтверждает
  same-key deduplication и per-group exclusion для pending same-CAS releases;
  process-level crash boundary дополнительно проверяет отдельный TS E2E через
  external-Caddy fixture; настоящая Caddy conformance остаётся открытой.
- Подтверждён TS E2E для `file:` config-secret refs: oversized regular file и
  directory отклоняются до запуска plugin, диагностика не содержит путь или
  содержимое. Прямое доказательство очистки всех копий secret buffers ещё
  отсутствует.
- Текущий focused group suite: archive, rollback, release-store и Management API
  tests проходят. После объединения параллельных изменений прошёл полный
  `make check` (Go build, 49 TS-файлов / 91 тест и Docker arch-lint без
  warnings), `go vet ./...`, Linux/macOS ARM64 builds и Gateway Docker smoke.
  Это не закрывает незавершённые production lifecycle/conformance пункты ниже.
- По разрешённому cleanup удалены старый `internal/infrastructure/network`,
  CompiledGraph/config DSL compiler и renderer, site/release registry и snapshot
  stores, их CLI/account store, GeoIP/MMDB runtime и telemetry exporters.
  В этом проходе также удалены неиспользуемые `WAFContext`, `WAFDecision`,
  `CapabilityClient.WAF` и соответствующая test-fixture ветка: вызовов не было,
  а generic HTTP response boundary уже обслуживает Caddy dispatch. Удалена
  локальная `contracts/v1/plugin-contracts.json` как дублирующая plugin IPC
  schema; единственный владелец этих контрактов — `pluginprotocol`. Исполняемый
  `OpenL4Stream` сохранён: его напрямую вызывает Caddy-L4 adapter.
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
  тесты на очистку буферов и отказ размера/типа файла. External Caddy теперь
  включает текущие plugin dispatch bindings в конфигурацию Caddy private Admin
  `/load`; Liapoldus handler вызывает plugin напрямую. Отдельного generation
  envelope или proxy RPC нет. Отказ атомарной загрузки Caddy покрыт; remote
  DispatchApply barrier и полный cross-variant conformance остаются открыты.
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
- [x] TS unit/integration/E2E suites покрывают bootstrap rejection, SQLite,
  group multipart/archive/rollback, native Caddyfile adaptation, external-Caddy
  lifecycle и direct Caddy-to-plugin dispatch. Отдельный real-child-process
  external Caddy-L4 тест проверяет TCP и UDP dispatch; embedded Caddy-L4,
  HTTP Stream/WebSocket/SSE и external restart/status также имеют исполняемые
  сценарии.
- [ ] Продолжать TS conformance для Caddy Admin checkpoint/drift/reconcile,
  production crash recovery, remote plugin replica barriers и общего
  embedded/external parity gate.
  Владелец утвердил cookie-policy control plane: отдельная SQLite policy на
  instance/capability и `GET`/`PUT` Management API с ETag/If-Match CAS и audit;
  `PUT` синхронно активирует candidate dispatch generation до SQLite commit и
  восстанавливает прежнее поколение при сбое. External Caddy подтверждает
  candidate через private Admin `/load` до durable commit. Embedded production
  `serve` child-process E2E уже проверяет
  восстановление policy, cookie allow-list, обычные и HttpOnly response actions
  и атомарный отказ без частичного `Set-Cookie`; отдельный real custom external
  Caddy E2E проверяет successful PUT, новое allow-list на активном public
  запросе и восстановление предыдущего поколения при SQLite CAS failure.
  Совместная конкуренция cookie PUT с group activation не имеет
  детерминированной тестовой точки: fixture не может наблюдать момент ожидания
  второго запроса на общем lock; таймерная проверка и production hook не
  добавлялись.
- [x] До реализации добавлены отдельные TS red/green suites для уже закрытых
  срезов multipart group publish/rollback, Caddy adapt/load, external Caddy
  lifecycle/direct dispatch, cookie boundary и stream/L4 dispatch. Удалённые
  legacy runtime suites не считаются эквивалентом нового покрытия.
- [ ] До оставшихся изменений добавлять TS red-tests в `tests/` для remote
  replica readiness/DispatchApply, Admin API checkpoint/reconcile, production
  crash recovery и каждого нового cookie/external-snapshot lifecycle перехода.

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
- [x] Исправить bootstrap deadlock первой `system` revision: при пустом current
  Management API оставался доступен, но publish отклонялся без Caddy activator.
  Lazy activator проверяет candidate до старта и открывает Caddy listener только
  при activation; TypeScript E2E проверяет publish, current revision, readiness
  и реальный HTTP response. Recovery/crash barrier и external parity отдельно
  остаются открытыми.
- [ ] Разработать SQLite migrations/repositories для groups/revisions/current/
  previous, plugin instances, service-key verifier metadata, operations,
  idempotency, audit, checkpoints и retention.
- [ ] Закрепить foreign keys, WAL, transaction isolation, локальный storage,
  backup/restore и crash recovery; SQLite хранит durable metadata, а
  Caddyfile/plugin settings revisions и большие artifacts остаются immutable
  files. На старте строить immutable in-memory RuntimeSnapshot; request path не
  читает SQLite/config files. Запретить dangling revision/artifact refs.
- [x] Выпускать service key через `POST /api/access/service-keys`: raw token
  возвращается только в исходном успешном ответе; verifier и metadata
  сохраняются вместе с audit row атомарно. Ошибка storage/audit возвращает
  только безопасную problem response.
- [x] Читать metadata-only список service keys через
  `GET /api/access/service-keys`; включать bootstrap и API-issued записи, не
  раскрывать verifier или raw token.
- [ ] Реализовать Management API security: private-network web backend доступ
  по mTLS + per-Gateway Bearer `platform-admin`; loopback/SSH-forwarded access
  по Bearer с TLS server verification. Gateway не реализует Constructor users
  или RBAC. Реализовать rotation/revocation и дальнейшую redaction policy.
- [ ] Обновить CLI: bootstrap status/migrations, service-key lifecycle,
  group inspect/rollback, Caddy build identity, drift/checkpoint/restore и
  reconcile.

## Caddy variants, groups и Admin API

- [ ] Собрать embedded Caddy и запускать compatible external custom Caddy binary
  как обязательные v1 variants; stock Caddy недопустим.
- [x] External-Caddy runtime запускается из `serve` с закрытым локальным
  control socket; настоящий custom Caddy fixture импортирует standard Caddy,
  Caddy-L4 и Liapoldus modules. Focused E2E проверяет direct plugin dispatch,
  startup/restart/status и сохранение прежнего snapshot при отказе `/load`.
  Embedded/external parity, module/build identity и полная deployment
  conformance остаются открыты.
- [x] External Caddy preflight вызывает штатный `caddy validate` для собранного
  candidate JSON до reservation/activation. Настоящий child-process E2E
  подтверждает, что несовместимый `capability → mode` отклоняется при
  provision Liapoldus-модуля и активный snapshot продолжает отвечать.
- [ ] Зафиксировать Caddy/xcaddy/module versions, build identity/module
  manifest, supply-chain verification и общий parity matrix.
- [x] Caddy-L4 TCP/UDP plugin dispatch прошёл focused E2E для embedded и
  supervised external custom Caddy. `external-caddy-l4-plugin-dispatch.test.ts`
  поднимает реальный child Caddy с Caddy-L4/Liapoldus modules через закрытый
  Admin `/load`, проверяет прямую TCP-передачу байтов и отдельные UDP streams
  для нескольких datagrams. Общий parity gate embedded/external variants на
  macOS/Linux остаётся обязательным. Провал блокирует v1, Go net/gnet fallback
  не разрешать.
- [ ] Довести native Caddyfile validation/adaptation, system group и стабильную
  composition application groups до production startup/recovery без Gateway
  route DSL.
- [ ] Довести multipart group release до полного v1: один безопасный `.tar.gz`,
  frontend roots, архивные limits/normalization/digests, plugin binding
  validation, immutable staging, full-snapshot activation и rollback current/
  previous. Текущий вертикальный срез принимает multipart, проверяет Caddy
  adaptation, CAS/idempotency в SQLite, сохраняет revision/operation и активирует
  composed snapshot через Caddy activator. Archive staging/manifest, rollback API
  и SQLite pointer swap реализованы и проверяются focused E2E. Отдельный
  concurrent HTTP E2E проверяет единственную pending reservation на группу:
  одинаковый idempotency retry возвращает ту же operation/revision, а другая
  публикация с тем же stale CAS получает conflict до второй активации. Новый
  `group-release-process-crash.test.ts` проверяет kill/restart реального Gateway
  между fixture-подтверждением Caddy `/load` и SQLite commit, включая публичный
  candidate response, неизменность current/previous, failed operation/journal и
  удаление staged файлов. Fixture эмулирует external Admin/data plane; это не
  заменяет conformance настоящего Caddy процесса и production Caddy activation.
- [x] TS integration фиксирует multipart
  `POST /api/groups/{id}/releases`, durable `OperationReference`, invalid
  Caddyfile, stale current revision, idempotent retry/conflicting key и reopen
  SQLite; отдельный concurrent test фиксирует same-key deduplication и
  per-group pending CAS exclusion. Archive/rollback покрыты focused tests.
  Production traffic activation остаётся за пределами API-fixture activator.
- [x] TS integration red-tests проверяют traversal rejection `422
  artifact_invalid` до activation и positive safe `.tar.gz` staging, archive
  digest и frontend manifest digest.
- [x] Добавить и исполнять archive vectors для gzip checksum integrity,
  duplicate path, case-fold collision и NFC normalization. NFC имена принимаются
  после нормализации; дубли/коллизии и повреждённый gzip дают `artifact_invalid`
  до изменения active revision.
- [x] Добавить и исполнять archive vectors для path depth/byte length,
  compression ratio и entry count. Все случаи отклоняются до активации; entry
  count vector использует directory headers, чтобы не выполнять 10 000 sync
  записей тестовых файлов.
- [x] Проверить compressed/uncompressed byte-limit ветки на уменьшенных порогах
  test fixture, сохранив production limits и не выделяя сотни MiB.
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
  `group-release-crash-recovery.test.ts` исполняет уже существующий service-level
  `Recover`: fixture создаёт durable reservation, активирует кандидат в fake
  activator, закрывает и повторно открывает SQLite, затем проверяет восстановление
  SQLite current composition, failed operation/journal, неизменность
  current/previous и удаление staged Caddyfile/archive. Отдельный
  `group-release-process-crash.test.ts` убивает реальный Gateway process после
  собранный `external-caddy-custom` применил candidate через свой настоящий
  Admin API и публично его отдаёт. Test-only Unix Admin proxy задерживает успешный
  `/load` response, поэтому Gateway ещё не дошёл до SQLite commit; до kill тест
  читает pending operation/journal и проверяет неизменные pointers. После
  перезапуска проверяются старый public snapshot, terminal failure journal и
  отсутствие pending или staged artifact. Тест доказывает этот process-crash
  boundary на настоящем Caddy, но не закрывает остальные crash points и полный
  v1 recovery/conformance gate.
- [x] Реализовать authenticated Admin API pass-through к закрытому Caddy Admin
  API, checkpoint перед каждой mutation, persistent checkpoint metadata и
  сравнение фактического runtime digest с последним checkpoint. Любой drift
  возвращает `409 group_drift_blocked` до staging group release; generation
  повторно проверяется непосредственно перед activation под общей блокировкой.
  TS integration доказывает, что failed publish не меняет current revision и
  число revision.
- [ ] Реализовать explicit checkpoint restore и full-composition reconcile с
  If-Match; group mutations остаются заблокированы при drift до одной из этих
  явных операций.
- [ ] Не обещать обратную генерацию Caddyfile из произвольного native Caddy JSON.

## Plugins, TLS и Constructor boundary

- Локальный запуск передаёт inherited listener и не использует argv,
  environment или application config file плагина; Gateway отправляет настройки
  конкретной revision через typed Bootstrap/ConfigApply до проверки readiness.
  Эту границу покрывает child-process TS E2E, но полный release gate ещё не
  пройден. Остаются: remote mTLS identity/revocation и endpoint sets;
  DispatchApply readiness barrier для remote replicas; management CRUD для
  launch settings; configurable restart/resource/grant policy. External Caddy
  передаёт local dispatch bindings и актуальную cookie policy в custom module
  через private Admin `/load`; remote dispatch barriers и общий
  embedded/external parity gate остаются открыты.
- HTTP `Stream` contract/handler фиксирует route concurrency, но отсутствуют
  configurable per-instance shared concurrency, idle-timeout и max-duration
  controls. Добавить их отдельным protocol/runtime contract + TS conformance;
  unary timeout не использовать для долгоживущих streams.
- [x] Cookie-policy production slice: SQLite schema v3, per-instance/
  capability CAS-store с audit в той же транзакции, Management `GET`/`PUT`,
  ETag/If-Match, schema validation и embedded/external Caddy dispatch generation
  activation с rollback при storage failure. Stream-only HTTP capabilities
  принимаются, TCP/UDP-only capabilities отвергаются. External Caddy получает
  полный candidate config через private Admin `/load` до SQLite commit.
  Production `serve` E2E закрывает восстановление сохранённой policy при старте embedded Caddy,
  реальный запрос через supervised child-plugin с allow-list, ordinary и
  HttpOnly response actions, а также атомарный отказ набора cookie actions без
  частичного `Set-Cookie`; тестовый seeder подготавливает только изолированную
  временную SQLite inventory и не затрагивает пользовательский Gateway.
  Отдельный real custom external-Caddy `serve` E2E подтверждает успешный PUT и
  новое allow-list правило на активном public request, а также SQLite trigger
  fault injection и восстановление прежней policy/snapshot после durable write
  failure.
  Остаются production conformance для rollback-failure fencing и совместной
  сериализации cookie PUT с group-release activation; fixture не наблюдает
  точку ожидания общего lock без production test hook.
- [ ] Завершить generic plugin-instance Management CRUD по
  [`management.openapi.yaml`](https://github.com/Liapoldus/liapoldus.github.io/blob/main/public/spec/management.openapi.yaml).
  Safe list и `GET /api/plugins/{pluginId}` реализованы из redacted in-memory
  inventory и проверяются child-process TypeScript E2E; ответы не содержат
  settings, endpoint или launch details. Остаются `POST`/`PUT`/`DELETE`, их
  version/CAS, lifecycle orchestration, audit и согласование с immutable
  dispatch generation. Пока write API отсутствует, cookie-policy API нельзя
  пройти на чистой установке: production E2E подготавливает instance/manifest/
  launch row во временной SQLite базе. Реализация должна оставаться generic и
  plugin-agnostic.

- Caddy handler валидирует capability invocation modes `call`, `http-stream`,
  `websocket`, `sse`, `tcp` и `udp`; mode capability tests есть. HTTP Stream,
  WebSocket и SSE focused E2E проходят 3/3, TCP/UDP — отдельный Caddy-L4 E2E.
  [x] HTTP Stream request-size conformance: TypeScript E2E читает канонический
  `maxRequestBytes` из `assets/contracts/caddy-http-stream.json`, отправляет
  настоящий HTTP/1.1 chunked body без `Content-Length` выше лимита и проверяет
  `413` до plugin response-start; child-plugin подтверждает, что переданный
  префикс не превышает лимит. Runtime уже считал фактически прочитанные bytes,
  поэтому production-изменение не потребовалось.
  Route concurrency guard уже есть, но configurable per-instance shared limit,
  idle-timeout и max-duration остаются TODO выше. Local `call` dispatch через
  embedded Caddy подключён к supervised child; candidate dispatch snapshots
  применяются для cookie-policy и group activation, но общего управления
  generation при dynamic instance lifecycle, local TLS settings и remote-replica
  fan-out ещё нет.
- [x] Добавить real-child-process smoke для локальных сборок captcha,
  forms-db и identity: Gateway `serve` выполняет Bootstrap/ConfigApply,
  запускает три процесса с inherited listener FD и передаёт capability-вызовы
  через embedded Caddy. Тест использует детерминированную CAPTCHA, memory
  storage forms-db и JWKS identity; plugin-клоны не изменяются. Это покрытие не
  закрывает generic plugin CRUD, remote plugins или общий cross-variant gate.
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
- [ ] Обобщить immutable dispatch generations на plugin lifecycle и remote
  replicas. Сейчас group/cookie-policy mutations синхронизируют candidate в
  embedded либо supervised external Caddy через private Admin `/load`, а ошибка
  preflight/load сохраняет прежний runtime; remote replica `DispatchApply`
  barrier и совместный crash/recovery gate остаются незавершёнными.
- [ ] Завершить capability→modes pre-activation validation для обоих вариантов.
  Embedded Caddy теперь вызывает non-starting `caddy.Validate`, provisions
  candidate dispatch app и проверяет route mode по live Manifest до Reserve;
  TS integration подтверждает синхронный отказ без operation/revision/pointer
  изменений и успешный matching `CALL`. External Caddy запускает штатный
  `caddy validate` над candidate JSON; real child-process E2E проверяет, что
  Liapoldus module provision читает live Manifest и несовместимый route mode
  отклоняется до `/load`, сохраняя прежний public snapshot. Временные plugin
  clients закрываются через Caddy `CleanerUpper`. Remote replica
  `DispatchApply` readiness barrier до activation пока не подключён.
- [ ] Расширять исполняемые golden-vector conformance: сейчас двадцать из
  двадцати одного vector исполняются через интеграционные фикстуры: bootstrap
  rejection, Management authentication, одиннадцать archive cases, три group
  publish cases (idempotency/single activation, idempotency-key conflict без
  смены revision и stale-CAS без смены pointer), activation failure, Admin
  checkpoint и drift-blocked group publish. Остаются remote-plugin
  no-downgrade и one-time credential reveal.
- [x] Исправить release workflow: пакетирование проверяет точный состав payload
  по `manifest.json`, SHA-256 каждого файла, отсутствие неописанных файлов и
  корректность путей вместо неподходящего фиксированного количества файлов.
- [ ] Завершить прямой Caddy gRPC `Call`/`Stream` production composition:
  настоящий supervised external Caddy child-process E2E теперь проверяет HTTP
  request/response chunks, WebSocket handshake/subprotocol/messages, SSE
  serialization, а отдельный сценарий — TCP/UDP L4 relay; embedded handler
  tests покрывают те же HTTP stream modes. Full serve generation lifecycle,
  cancellation/backpressure/limits, remote replicas и общий deployment parity
  остаются не закрыты end-to-end.
- [ ] Не объявлять v1 готовым без полного cross-variant HTTP/TLS/ACME/L4/plugin/
  SQLite/recovery/security/admin-proxy conformance.
