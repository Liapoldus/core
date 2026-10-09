# TODO — Core v1

## Подготовительная чистка перед v2 — 2026-10-09

- [x] SQL storage adapters и SQLite schema/migrations перенесены в Go с
  сохранением именованных запросов, bytes, версий и порядка; файловые SQL
  parsers удалены. Пять наборов storage constants перенесены в typed Go.
  Native regression tests фиксируют исходные query/schema/definition digests.
- [x] Native Go tests подключены к `make test`; прежний запрет Go tests удалён
  из архитектурных проверок и инструкций. TS HTTP/CLI/E2E coverage сохранён.
- [x] Устранена зависимость plugin transport adapter от config adapter:
  schema validation явно передаётся composition root и fail-closes без неё.
- [x] Targeted storage/config tests, race и vet прошли; семь targeted Vitest
  suites (17 tests) прошли. Это не общий release gate.
- [ ] Независимая сборка: Core закрепляет SDK v1.0.0 без используемых v2 API.
  Локальный workspace не закрывает этот gate; нужна опубликованная совместимая
  SDK revision. Коммит/публикация не выполнялись.
- [ ] ENV-only bootstrap, однократный `core init`, собственные SQLite settings
  API с CAS/pending/effective, offline migration/recovery, removal static
  registry fallback и полный blocking strict lint ещё не реализованы.
  Существующие runtime YAML loaders пока нельзя объявлять удалёнными.
- [ ] Публичные schema/error catalogs ещё не полностью code-generated;
  миграция внутренних storage constants не означает завершение всех contracts.

Нормативный scope: [целевая архитектура](https://liapoldus.github.io/core/architecture/target), [Control Plane](https://liapoldus.github.io/core/architecture/control-plane), [ручной запуск v1 / автоматизация v2](https://liapoldus.github.io/core/architecture/plugin-deployment), [roadmap](https://liapoldus.github.io/core/architecture/v1-migration-roadmap) и [acceptance](https://liapoldus.github.io/core/configuration/acceptance).

## Документация

- [x] Канонические Core Markdown и Mermaid исходники принадлежат этому repo в
  `docs/site/core/` и `docs/site/diagrams/`. Агрегатор `liapoldus.github.io`
  собирает закреплённый commit SHA в единый сайт без редактируемой копии.
- [x] После изменения Core docs синхронизировать pinned SHA
  `2c58af3e3e2a121cdf8b085baed209ba64cd8d46` в
  `liapoldus.github.io/docs-sources.json`; `docs:sync`/VitePress build прошли,
  GitHub Pages deployed (маршруты Core, protocol, Server и forms-db отвечают 200).

## Текущий статус — 2026-10-02

Дополнение 2026-10-04: уточнённый owner contract для синхронных Admin Actions
подтверждён отдельным успешным повтором в `plugin-admin-surface-forwarding`:
два принятых запроса с одним `Idempotency-Key` оба достигают выбранной replica,
получают разные request ID и создают отдельные audit-записи. Ключ — только
correlation metadata; Core не дедуплицирует действие. Targeted Vitest прошёл на
macOS и в Ubuntu 24.04.5 ARM64 VM под OrbStack; Core `make check`, `go vet
./...` и `make staticcheck-u1000` после изменения прошли. Это не exactly-once:
неизвестный результат сетевого вызова автоматически не повторяется.

Дополнение 2026-10-04: исправлена оставшаяся ссылка на удалённый wire namespace
`liapoldus.plugin.v1` в целевой архитектуре Core: указан актуальный generic
`liapoldus.peer.v1`; тестовая manifest fixture больше не притворяется старым
protocol manifest. `serve-plugin-runtime.test.ts` прошёл. VitePress `docs:sync`
и `npm run build` прошли на локальных checkout. На тот момент owner worktree
ещё не был закоммичен; 2026-10-05 v1 prompt разрешил публикацию после gates.

Дополнение 2026-10-04: `serve-plugin-runtime.test.ts` теперь отдельно доказывает,
что active generation с недоступным declared endpoint не блокирует запуск Core:
Management health доступен, status честно сообщает `not-ready`/`drift: true`.
Targeted child-process тест прошёл на macOS и OrbStack Ubuntu; после добавления
регрессии повторно прошли полный Core `make check` (70/128, 1 skip), `go vet
./...`, `make staticcheck-u1000` и `git diff --check`.

Дополнение 2026-10-04: Core SQLite schema v9 и plugin inventory больше не
содержат deployment `mode` или plugin `endpoint`. Startup migration атомарно
перестраивает старую `plugin_instances`, сохраняя manifest/state, конфигурации
active/previous/staging и связанные replica observations; после миграции
проверяется referential integrity. Проверено повторным запуском v5 fixture и
inventory integration. Core `make check` прошёл (70 файлов Vitest, 128 тестов,
один файл пропущен; Docker architecture lint), `go vet ./...`,
`make staticcheck-u1000`, `go build ./...` и `git diff --check` прошли.
Остальные release gates ниже остаются открытыми.

Повторная межрепозиторная проверка 2026-10-04: Core SQL-backed production E2E
прошёл 3/3 на одноразовых PostgreSQL 16, MySQL 8.0 и MariaDB 11.4; containers
созданы только для этого прогона и остановлены. SDK 14/190, pluginprotocol
9/138 + race 9/138, Server 30/79, forms-db 20/33 + Node 2/2 прошли актуальные
suites и Go build/vet. Позднее все наборы повторно прошли в OrbStack Ubuntu
guest; результаты описаны ниже. VitePress `npm run docs:sync && npm run build`
прошёл по локальному workspace. Hosted CI, опубликованные docs pins и release
artifact остаются открытыми.

Дополнение 2026-10-03: production Core→Server→forms-db SQL walkthrough
проверил отказ недоступного candidate, терминальный `failed` у операции,
сохранение прежнего serving runtime и восстановление `submit`/`list` после
rollback. PostgreSQL 16, MySQL 8.0 и MariaDB 11.4 прошли `3/3` на disposable
контейнерах. Причина прежнего `503` была в panic forms-db при вызове `Close`
на typed-nil SQL repository; исправление и отдельная регрессия находятся у
владельца forms-db. Core `make check` после среза прошёл (70 файлов /
128 тестов, один файл пропущен, Docker arch-lint), `go vet ./...`,
`make staticcheck-u1000` и `git diff --check` также прошли. Это дополнение
не заменяет полный release gate ниже.

Дополнение 2026-10-03: настоящий Core mTLS API выдал forms-db одноразовый
secret grant для active generation 1; после успешного Reload generation 2 тот
же grant был отклонён контрактным `grant_denied`. Ответ, Core logs и durable
SQLite/audit state не содержали secret handle. Production Core→Server→forms-db
SQL walkthrough прошёл 3/3 на PostgreSQL 16, MySQL 8.0 и MariaDB 11.4.
Семантика синхронных plugin Admin Actions закреплена отдельно от durable
operation idempotency: одинаковый `Idempotency-Key` не дедуплицирует Admin
Action, каждый принятый запрос выполняется и аудируется заново; это не
exactly-once и неизвестный результат автоматически не replay-ится.

Дополнение 2026-10-03: production `forms.list` теперь также доказывает
отклонение generation-2 plugin при active generation 3: Core принимает
настройки, не может уведомить forms-db из-за недоступного объявленного REST
endpoint, а живой Server→forms-db peer вызов завершается только безопасным
`503 storage_unavailable` без DSN, cursor secret или operation ID. После
возврата endpoint Core повторно уведомляет plugin, и `forms.list` восстанавливается.
Проверено в `manual-core-server.test.ts` (1/1) и SQL-backed
`manual-core-server-sql.test.ts` (3/3: PostgreSQL 16, MySQL 8.0, MariaDB 11.4;
одноразовые контейнеры удалены). Это закрывает только данный stale-generation
путь. Дополнительно Core CRL отзывает forms-db replica client certificate,
после чего реальный forms.list не может получить scoped grant и fail-closes;
после возврата CRL Core/plugin сходятся. Это проверено на memory и SQL-backed
production E2E (PostgreSQL 16, MySQL 8.0, MariaDB 11.4, 3/3). Полный
security-negative matrix остаётся открытым. После изменения прошли `make check`
(70 файлов Vitest, 128 тестов, 1 пропущенный;
Go build и Docker arch-lint), `go vet ./...`, `make staticcheck-u1000`,
`go test ./...` и `git diff --check` на macOS/arm64.

Повторный прогон текущего дерева: `make check` прошёл (70 файлов / 128 тестов;
один test file skipped; Docker architecture lint OK), `go vet ./...` и
`make staticcheck-u1000` прошли. Дополнительно прошёл реальный
`tests/integration/manual-core-server.test.ts` walkthrough (Core→Server→forms-db,
включая settings generation/rollback, прямой submit/list, site publish, ручные
restart/reconnect, backup и отклонение restore при работающем Core). Внутри
walkthrough Core останавливается при работающих плагинах: forms list через
Server fail-closes как `storage_unavailable` при недоступном Core scoped-grant
endpoint и восстанавливается после ручного запуска Core.

Core проходит `make check` (70 файлов / 128 тестов, включая Docker arch-lint; один
существующий test file skipped),
`go vet ./...` и `make staticcheck-u1000`. Реальный ручной E2E Core→Server→
forms-db прошёл: exact settings, Reload/pull/ACK, HTTP generation change,
разрешённый прямой Server→forms-db submit по pluginprotocol+mTLS, `forms.list`,
rollback, Server/forms-db restart, artifact publish и site persistence after
restart. Core-backed PostgreSQL E2E подтвердил SQL persistence после forms-db
restart и восстановление Server peer-соединения без replay вызова; такой же
production E2E 2026-10-02 прошёл на MySQL 8.0 и MariaDB 11.4. Cross-repository
CI настроен запускать все три SQL backend.
В E2E дочерние Core/Server/forms-db процессы не получают SQL DSN из
test-selector environment variables; проверка это утверждает явно.
E2E отдельно останавливает Server при работающем Core, проверяет переход в
degraded/drift, запускает его снова и подтверждает, что read-only monitor не
повторяет Reload: replica остаётся degraded. Затем E2E вручную перезапускает
Core; startup reconciliation выполняет exact-generation Reload/pull/ACK, после
чего `/api/status` возвращает `ready` и `drift: false`. Core не объявлять
production-ready до полного release gate:
Linux VM runtime matrix, документационная синхронизация, CI и semver
релизный набор не закрыты. Server certificate `list/get` реализованы как
read-only actions; ручные certificate renew/revoke actions исключены из v1,
Caddy автоматически продлевает сертификаты.

## Зафиксированная граница v1

- Состав v1: три сервиса — Core, Server plugin, forms-db plugin; две библиотеки —
  Plugin SDK и `pluginprotocol`. CAPTCHA и Identity отложены до v3 и заморожены.
- Оператор отдельно устанавливает и вручную запускает Core и оба plugin
  binaries. Core не устанавливает, не запускает, не останавливает, не
  перезапускает, не масштабирует и не удаляет plugin processes или containers.
- Core подключается только к явно зарегистрированным fixed endpoints, проверяет
  per-replica mTLS identity, Manifest/schema и health/readiness. Он публикует
  exact-generation config endpoint для SDK REST pull; сам Core только вызывает
  `Reload(generation)` и не передаёт settings payload в notification.
- Docker/Compose, Swarm, Kubernetes как внешнее размещение — v2;
  Установка и плановые обновления Core/plugins принадлежат оператору;
  Core process supervision
  не планируется. Не включать их в
  v1 API, SQLite schema, permissions, CLI или acceptance.
- Plugin configuration — точные raw JSON bytes. Durable slots три:
  `active`, `previous` и внутренний `staging`. Только первые два когда-либо
  отдаются реплике. `staging` хранит validated candidate, связанный с durable
  operation, для recovery; он не является видимым config generation.
  Решение владельца принято и не пересматривается (см. «Решение владельца по
  `staging`»). Core не decode/remarshal-ит и не знает product fields.
- После validation одна SQLite-транзакция сдвигает текущий `active` в
  `previous`, записывает candidate как новый `active` и удаляет прежний
  `previous`. После commit Core публикует in-memory snapshot и вызывает REST
  `Reload`; plugin сам pull-ит точный generation. Partial rollout идёт вперёд,
  с per-replica ACK/fencing; rollback меняет `active`/`previous` и вызывает
  обычный Reload.
- Core использует только Plugin SDK REST и не импортирует `pluginprotocol`.
  `pluginprotocol` — configurable generic plugin↔plugin library без Core
  lifecycle или product contracts. Core остаётся plugin-agnostic.
- CAPTCHA и Identity остаются вне v1/v2; их repositories заморожены до отдельной
  явной разморозки для v3.
- Server plugin v1/v3 public scope не включает Caddy-L4 и TCP/UDP relay; эти
  возможности относятся к v3.

## 1. Core config и durable operations

- [x] Завершить generic `PUT /api/plugins/{id}/settings`: принимать сам JSON
  object без wrapper, сохранять точные UTF-8 request bytes, вычислять SHA-256 по
  ним же; проверять size, syntax, root type, duplicate keys и plugin-owned JSON
  Schema без изменения bytes. Покрыто `plugin-settings-api`,
  `plugin-settings-mutation`, `golden-vector-conformance`.
- [x] Свести конфигурационное durable storage к `plugin_config_generations` с
  колонками `instance_id`, `generation`, `slot`, `raw_json BLOB`, `sha256`,
  `schema_version`, `created_at`; разрешены `active`/`previous`/`staging`, не
  более одной строки на слот. Старые stores удалены, `plugin_config_revisions`/
  `plugin_config_pointers` отсутствуют. Покрыто `sqlite-metadata`,
  `plugin-config-migration`.
- [x] Реализовать CAS, `Idempotency-Key`, durable operations и аудит для
  settings, rollback и plugin Admin Surface. Candidate validation failure не
  меняет slots и не вызывает Reload. Покрыто `operation-persistence`,
  `plugin-settings-mutation`, `sqlite-audit-retention`.
- [x] Реализовать atomic `active`/`previous` update до Reload, per-replica ACK,
  roll-forward/fencing, explicit rollback и crash recovery; неизвестный outcome
  не replay-ится. Покрыто `plugin-config-roll-forward`, `plugin-settings-recovery`,
  `plugin-settings-rollback`, `plugin-configuration-revisions`.
- [x] Построить immutable in-memory snapshot из SQLite до readiness; runtime
  request path не читает SQLite или конфигурационные файлы.
  **ПРОВЕРЕНО 2026-09-30:** exact-generation pull читает атомарный снимок
  `active`/`previous`; readiness/drift читают отдельный опубликованный снимок
  desired generation и ACK-наблюдений. После startup recovery оба снимка
  обновляются до открытия listener-ов. Покрыто
  `plugin-config-snapshot`, `plugin-convergence-snapshot`,
  `serve-plugin-runtime` и полным `make check`. Сквозную проверку restart с
  реальными plugin-процессами учитывать в отдельном v1 acceptance gate.
- [x] Завершить SQLite backup/restore CLI, integrity/foreign-key checks,
  migrations и source-owned embedded SQL. Не добавлять PostgreSQL или S3.
  **ПРОВЕРЕНО 2026-09-30: частично.** Embedded source-owned SQL, `schema_migrations`
  и проверка версии есть; апгрейд v6→v9 идёт идемпотентным применением схемы
  (`sqlite.go:64` допускает меньшую версию, `sqlite-version-guard` это
  закрепляет). На production bootstrap добавлены `quick_check(1)` и
  `foreign_key_check` до/после schema apply; исполняемый тест отклоняет
  осиротевший FK до открытия listener-ов. **РЕАЛИЗОВАНО:** `core database
  backup` создаёт online snapshot через `VACUUM INTO`, проверяет schema,
  integrity и foreign keys и публикует файл с режимом `0600`, не перезаписывая
  существующий destination. `core database restore` проверяет backup и атомарно
  заменяет state; работающий `serve` удерживает эксклюзивную блокировку, поэтому
  restore при активном Core отклоняется. Сквозной CLI тест проверяет данные,
  замену существующего файла, повреждённый backup и конфликт lock. Operator
  procedure: `docs/site/core/deploy/backup-restore.md`.

## 2. Plugin SDK REST integration

- [x] Перевести Core imports и Go module dependency на утверждённый
  `github.com/Liapoldus/plugin-sdk`; SDK и Core `go build ./...` проходят.
  Fan-out `Reload` одновременно оповещает все объявленные replica и не ждёт
  завершения одной перед вызовом остальных; отказ одной не отменяет остальные
  вызовы, каждый результат фиксируется отдельно. Покрыто
  `tests/integration/plugin-reload-fanout.test.ts`.
- [x] ~~**Подтверждено на штатном бинаре: `core serve` полностью игнорирует
  `pluginControl`.**~~ **Исправлено (2026-09-30).** `core serve` теперь сам
  собирает control plane из `core.yaml`: `RunOptions.PluginRESTControl` удалён,
  `bootstrap.serveBootstrap` вызывает `buildPluginRESTControl(...)` и
  `startPluginRESTControl(...)` безусловно, поэтому `pluginControl.listen`
  действительно слушает, а `SDKConfigurationApplier.Clients` заполнен
  per-instance fan-out. Покрыто: `tests/integration/serve-plugin-runtime.test.ts`,
  `tests/integration/bootstrap-relative-paths.test.ts`,
  `tests/integration/bootstrap-loader.test.ts` (7 negative-случаев).
- [x] ~~**Ложная готовность.**~~ **Исправлено (2026-09-30).** `/api/status`
  больше не возвращает захардкоженный `drift: false`: `Server.DataPlaneDrift`
  вычисляется из production-эвалуатора (`pluginDrift`/`pluginReadiness` в
  `plugin_registration.go`) по наблюдениям `plugin_replicas`, а при отсутствии
  источника evidence handler **fail-closed** отдаёт `drift: true`.
  Невосстановимая Core-конфигурация и отсутствующий/невалидный
  `pluginControl` trust material блокируют startup. Недоступность plugin replica
  сама по себе startup не блокирует: Core поднимается, фиксирует недоступную
  replica, а после появления desired generation сообщает degraded/not-ready и
  drift до её convergence. Покрыто:
  `tests/integration/management-api-characterization.test.ts` (drift: true без
  evidence), `tests/architecture/presentation-boundaries.test.ts`,
  `tests/architecture/v1-migration-contracts.test.ts` и
  `tests/integration/serve-plugin-runtime.test.ts`: настоящий Core с active
  generation стартует с закрытым replica endpoint, `/healthz` остаётся доступен,
  а `/api/status` показывает `not-ready` и `drift: true`.
  Promotion-before-fan-out в `plugin_configuration_service.go` сохранён
  намеренно: durable `active`/`previous` двигается в той же транзакции, что и
  раньше, а компенсация деградации обеспечена fencing + наблюдениями, а не
  откатом слота. Это отдельное решение владельца — см. «Требует решения
  владельца» ниже.
- [x] Завершить Core REST client/server adapters: fixed endpoint для каждой
  replica, per-replica mTLS (раздельные `replicaClientCA` для pull и
  `replicaServerCA` для dial), `Reload`, exact config pull и digest-bound ACK.
  Покрыто `plugin-sdk-config-pull`, `serve-plugin-runtime`.
- [x] Подключить SDK к реальному Core composition root и удалить Core lifecycle
  dependency/wiring через `pluginprotocol`. `go.mod`/`go.sum` не содержат
  `pluginprotocol`; SDK вызывается только через `internal/infrastructure/plugins`.
  Границы проверены `make arch-lint` и `pluginprotocol-sdk-boundary`.
- [x] Проверить ручное восстановление после restart plugin: E2E подтверждает,
  что самостоятельный restart при работающем Core оставляет replica degraded,
  а ручной restart Core выполняет startup reconciliation и восстанавливает
  exact active generation через mTLS readiness/Reload/pull/ACK. В v1 нет
  background retry; Core не запускает plugin и не повторяет пользовательские
  вызовы.
- [x] Проверить Management authorization, service-key redaction, атомарность
  выпуска ключа вместе с audit и срок хранения audit. Evidence:
  `golden-vector-management-auth.test.ts`, `service-key-list.test.ts`,
  `service-key-issuance.test.ts` и `sqlite-audit-retention.test.ts`.
- [x] Проверить отказ отозванной workload identity в Core↔Plugin SDK mTLS:
  `tests/integration/manual-core-server.test.ts` запускает Core и plugins с
  отдельными trust roots/подписанным CRL, подтверждает успешный exact-generation
  pull до отзыва и TLS-отказ после добавления serial в CRL и orderly restart
  Core. Это доказывает CRL enforcement на Core pull listener. Полная coordinated
  downtime rotation также исполняется тем же реальным Core→Server→forms-db
  fixture: Core и обе plugin replicas останавливаются вручную, сертификаты и
  Core↔plugin/peer trust root заменяются, после чего все процессы запускаются
  снова; новые credentials сходятся, старый root отклоняется. Горячая ротация
  по-прежнему не поддерживается и не входит в v1.
- [x] Реализовать Core-side SDK secret-grant issue/redemption endpoints:
  one-use grants bound к аутентифицированной replica, candidate/active
  generation, opaque reference и purpose; значение выдаётся только через
  scoped redemption и не попадает в API/log/error. Проверено
  `plugin-secret-grants.test.ts` и настоящим Core→Server mTLS Reload с
  custom certificate grants.

## 3. Plugin registration и product integration

- [x] **Решение владельца принято (2026-09-30): вариант A.** `core.yaml` —
  единственный нормативный источник declared plugin topology. Утверждённые
  правила: `plugins[].instanceId` + `plugins[].replicas[].replicaId`,
  фиксированный HTTPS `endpoint` и `expectedPeerIdentity`
  (`commonName` + optional `uniformResourceIdentifier`); список статичен на
  время процесса, изменение = restart; Management API регистрации и plugin
  self-registration в v1 **нет**; endpoint никогда не берётся из redirect,
  manifest или ответа plugin. Варианты B и C отклонены.
  Реализовано: `assets/contracts/core.schema.json` (`plugins` — обязательный
  корень), `config-fields.yaml`, `LoadBootstrap` → `pluginInstances(...)`,
  `config.PluginEndpoint(...)`, `config.ValidPeerIdentity(...)`,
  `internal/infrastructure/plugins/declared_replicas.go` (единственное место,
  где объявленные endpoint+identity превращаются в живой SDK client),
  `plugin_control_plane.go`, `plugin_registration.go`, таблица `plugin_replicas`.
  SQLite не хранит endpoint/identity/replica set: реплики и наблюдения — только
  из `core.yaml`, таблица `plugin_replicas` содержит исключительно
  observation/ACK-состояние.
- [x] Удалить исторические `plugin_instances.mode` и `plugin_instances.endpoint`
  из схемы и inventory v1. Миграция перестраивает legacy parent table в одной
  транзакции, сохраняет дочерние наблюдения и поколения, затем проверяет
  foreign keys; интеграционный fixture подтверждает сохранность raw JSON и
  repeat startup. Inventory API не раскрывает deployment mode.
- [x] ~~Довести Management API для CRUD generic plugin instance/replica fixed
  endpoints и ожидаемых identities~~ **Снято решением A:** объявление topology
  перенесено в `core.yaml`, поэтому Management API регистрации не появился и
  добавлять его не нужно. OpenAPI/docs локально синхронизированы с этим
  решением: registration/mutation, interaction и cookie-policy paths удалены;
  остаются только читаемые plugin instances, settings, rollback, Admin Surface,
  operations, audit и access. Docs commit `453b15e` опубликован в `main`;
  Pages deployment после этого commit нужно проверить отдельно.
- [x] Проверить интеграцию вручную запускаемого `plugins/server`: настоящий
  Core→SDK→Server child-process smoke подтвердил PUT, точные поколения,
  Reload/pull/ACK, изменение HTTP response, rollback, restart Server и
  последующий Core restart со сходимостью по `ready`/`drift: false`.
  Дополнительный Server child-process test подтвердил scoped custom TLS grants.
  Core→Server site-publish child-process E2E подтвердил multipart upload без
  `If-Match`, durable operation, serving результата и сохранность после restart.
- [x] Удалить Caddy-L4 dependency/registration и public TCP/UDP listener/relay
  implementation из v1; Caddy-L4 и relay остаются вне v1, запланированы в v3.
- [x] Проверить forms-db plugin suite и SQL adapters: настоящий Core→Server→
forms-db process pair проверяет authorized HTTP submit через прямой peer
dispatch; отдельный forms-db child-process suite с Core-compatible SDK
fixture проверяет Reload/pull/ACK, grants, peer submit и SQLite restart.
PostgreSQL 16, MySQL 8.0 и MariaDB 11.4 repository и child-process suites
прошли на disposable DB containers. Core-backed E2E проверяет Settings/Reload,
submit/list через Server и сохранность записи после ручного forms-db restart;
Server сбрасывает потерянную peer-сессию и переподключается на следующем
независимом вызове без replay. PostgreSQL был проверен ранее; 2026-10-02
настоящий Core→Server→forms-db сценарий дополнительно прошёл на MySQL 8.0 и
MariaDB 11.4 (2/2). Production Core→Server→forms-db E2E теперь также выполняет
24 параллельных HTTP submissions и bounded cursor pagination на каждом из трёх
SQL backend; PostgreSQL 16, MySQL 8.0 и MariaDB 11.4 прошли повторно.
Cross-repository CI запускает все три backend.
- [x] Реализовать generic bounded artifact forwarding в Management API через
  Plugin SDK REST stream. Core не импортирует peer-only `pluginprotocol`, не
  интерпретирует Server archive/site payload и не буферизует весь artifact;
  Server владеет publish metadata/receipt, валидацией и durable operation.
- [x] Доказать artifact forwarding из Core до настоящего Server child process
  и подтверждённую Server receipt/operation. Исправлены SDK transport adapters
  для artifact/action deadlines и page binding операции status.
- [x] Не добавлять plugin-name/capability branches в Core. Не мигрировать и не
  собирать как Core v1 CAPTCHA/Identity.

## 4. Проверки и удаление старого lifecycle

- [x] Добавить Core TypeScript integration/E2E для raw bytes, slots,
  CAS/idempotency, Reload/ACK, fencing/recovery, authorization/audit/redaction,
  secret grants, Admin Surface/artifact forwarding, Core→Server/forms-db и
  ручного Core process restart с проверкой plugin convergence; тесты должны
  оставаться в `tests/`.
- [x] Удалить старые Core process supervision и provider installation API,
  SQLite provider/workload state, неиспользуемые CLI commands и legacy protocol
  lifecycle. Регрессии закреплены `pluginprotocol-sdk-boundary`,
  `no-legacy-site-api`, `target-cli-surface`, `dead-artifacts`.
- [x] Синхронизировать versioned contracts/mirrors, OpenAPI, docs, this TODO и
  `AGENTS.md`; не оставлять конкурирующие lifecycle APIs.
  - [x] ~~**Рассинхрон published OpenAPI (2026-09-30)~~ **Решено владельцем:
    контракт описывает только реализованное v1.** Из
    `contracts/v1/management.openapi.yaml` удалены несуществующие и запрещённые
    поверхности: `POST /api/plugins` (createPlugin),
    `PUT`/`DELETE /api/plugins/{pluginId}`, `/interactions`,
    `/cookie-policies/{capability}`, `/service-keys/{keyId}/rotate` и `/revoke`;
    путь admin surface исправлен с `/api/plugins/{pluginId}/admin/surface` на
    фактический `/api/plugins/admin-surfaces`. Вместе с ними удалены осиротевшие
    `PluginCreate`, `PluginDesiredState`, `PluginReplicaInput`, `InteractionPolicy*`,
    `InteractionEdge`, `CookiePolicy*`, `CookiePreconditionRequired`,
    `InvalidCookiePolicy`, `IfMatchOptional`; 16 paths → 12 paths / 14 operations.
    Ложные формулировки (`supervised lifecycle завершается Core`, `supervised
    принимает catalog release reference`, `push-ится по pluginprotocol ConfigApply`)
    заменены на declared-registry + SDK REST Reload. Перепубликовано через
    `scripts/publish-contracts.mjs` (обновлён `manifest.json`).
    Новый гейт `tests/architecture/management-openapi-surface.test.ts` фиксирует
    operation set против роутера, запрет lifecycle/registration и целостность
    `$ref`; `v1-migration-contracts.test.ts` переписан под решение. Проверено
    негативными прогонами: возврат запрещённого пути, висячий `$ref` и остаточное
    слово `supervised` — каждый даёт падение.
  - [ ] **Вынесено в v2 backlog (решение владельца: не реализовывать в v1).**
    Plugin-to-plugin interaction policy (outbound allow-list с generation/CAS),
    interaction grants, cookie policy (per-instance/capability allow-list,
    428 без If-Match) и service-key rotate/revoke не входят в v1 Management API.
    Они не тождественны scoped secret grants, которые нужны v1 через отдельный
    Plugin SDK REST control endpoint для secret references. Если v2 features
    понадобятся, они
    требуют новой durable схемы (interaction generations, cookie policies,
    revokedAt-ротация) и расширения Management API — отдельным инкрементом.
    `AGENTS.md` теперь явно разделяет v1 secret-grant broker и отложенные v2
    plugin-to-plugin interaction policy/grants.
  - [x] **Синхронизация нормативного docs OpenAPI/error contract выполнена в
    рабочем дереве 2026-09-30.** Удалены неподдерживаемые registration,
    interaction, cookie-policy и service-key rotation/revocation operations;
    оставлены текущие маршруты Core, включая `/healthz` и
    `/api/plugins/admin-surfaces`. Error catalog приведён к v1 contracts и
    обновлён hash в manifest. Публикация остаётся отдельным gate: сначала
    VitePress build прошёл 2026-09-30; docs commit `453b15e` отправлен в `main`.
    Core `npx vitest run tests/architecture/contract-publication.test.ts`
    прошёл 2026-09-30 (1 file / 1 test).
- [x] Прогнать Core `make check`, `go vet ./...` и `make staticcheck-u1000`
  на macOS 2026-10-02; последний `make check` прошёл: Go build, 70 Vitest files,
  1 skipped / 128 tests, Docker architecture lint; `go vet ./...`,
  `make staticcheck-u1000` и `git diff --check` также прошли. Реальный
  Core→Server→forms-db тест плановой замены Core/plugin leaf credentials и
  control/peer trust roots прошёл на memory и PostgreSQL 16, MySQL 8.0,
  MariaDB 11.4; после замены новый root сходится, старый отвергается.
- [x] Linux runtime matrix и сквозные проверки: Ubuntu 24.04.5 ARM64 VM под
  OrbStack прошла Core `make check`/vet/staticcheck, SDK и protocol suites
  (включая protocol race gate), Server suite/build/vet и forms-db suite/build/vet.
  Настоящие Core, Server и forms-db процессы прошли manual walkthrough на
  memory и на PostgreSQL 16, MySQL 8.0 и MariaDB 11.4; SQL matrix — 3/3, включая
  concurrent submit/list, candidate refusal/rollback, outage/recovery,
  persistence, grants, redaction и отсутствие DSN у дочерних процессов.
  Server HTTP/1.1–3, ACME Pebble, site publish, WebSocket и SSE также проверены
  в guest Linux. Отдельный bare-metal host не является v1 gate.
- [x] Локальные финальные hygiene/docs проверки этого среза: `git diff --check`
  прошёл во всех пяти owner repos; `npm run docs:sync && npm run build` прошёл
  для VitePress на локальных worktrees.
- [x] Пройти ручной security/recovery walkthrough на Linux VM:
  `go run ./tests/fixtures/manual-core-server` выполнен из смонтированного
  workspace внутри OrbStack VM, exit 0. Он подтвердил settings/rollback, HTTP
  generation switch, site publish/restart persistence, forms submit/list/Admin
  Surface/cursor, manual reconnect, mTLS rotation/revocation, redaction и
  отсутствие peer payload в Core. Отдельные SQL E2E прошли на PostgreSQL 16,
  MySQL 8.0 и MariaDB 11.4.
- [x] Закрыть внешние gates: hosted cross-repository CI после публикации
  Server/forms-db, docs pins и release metadata завершены.
  Plugin SDK `v1.0.0` и `pluginprotocol/v2 v2.0.0` опубликованы; protocol Go
  module major v2 отражает breaking API, wire namespace остаётся
  `liapoldus.peer.v1`. Server и forms-db используют опубликованные версии без
  локальных `replace` (`GOWORK=off` build/vet пройдены). Локальный
  `manual-core-server` cross-process E2E прошёл 2026-10-05; Core verify и
  cross-repository integration прошли на commit `2c58af3`, включая SQL-backed
  сценарии. `core-v1.0.1` опубликован, release contracts archive создан.

## V2 — milestones, вне приёмки v1

Нормативные последовательности и границы: [размещение plugins](docs/site/core/architecture/plugin-deployment.md).
Реализация каждого пункта начинается отдельным TypeScript red-test; Core не
импортирует `pluginprotocol` и не получает product-specific веток.

### 1. Discovery и rollout — владелец Core + Plugin SDK

- [x] Реализовать в локальном Core WIP authenticated per-replica
  registration/renew/deregister: уникальный
  incarnation, SAN/SPIFFE binding, immutable endpoints/placement, SemVer/digest,
  compatibility ranges, lease 30 s с renew 10 s. Живые endpoints — in-memory;
  после рестарта требуется новая регистрация. Локальный child-process gate
  restart/expiry/identity mismatch, duplicate registration и stale incarnation
  подтверждён командами, перечисленными в handoff этого пункта. Это не clean
  release gate: Core по-прежнему зависит от registration/directory API локального
  Plugin SDK WIP, отсутствующих в опубликованном pinned SDK; обычные
  `GOWORK=off` build и hosted cross-repository gate остаются открытыми.
  В текущем локальном WIP уже есть mTLS REST handler, SAN/lease directory и
  SQLite admission; запись instance+replica транзакционная, а live entry
  публикуется только после commit. Уже зарегистрированные истёкшие,
  deregister-нутые и заменённые identities fenced от legacy resolver fallback.
  Outbound REST TLS теперь дополнительно pins точный ожидаемый CN/URI SAN после
  стандартной проверки CA/hostname и revocation; это покрыто
  `tests/integration/plugin-peer-identity.test.ts`. Локальный WIP теперь строит
  `Reload` fan-out из текущего lease snapshot, исключает просроченные leases,
  закрывает уже созданные клиенты при неполном snapshot и не откатывается на
  статические clients для уже зарегистрированного, но недоступного instance.
  Новая регистрация вызывает `Reload` текущего active поколения только для
  зарегистрированной replica, без N² fan-out при scale-out; plugin затем сам
  получает точные bytes через private mTLS pull API. Это покрыто
  `tests/integration/registered-replica-reload.test.ts`,
  `tests/integration/plugin-replica-directory.test.ts` и реальным mTLS pull в
  `tests/integration/plugin-sdk-config-pull.test.ts`; fan-out проверяет точное
  совпадение ACK с requested generation/digest/schema и считает несовпадающий
  ACK protocol violation (`tests/integration/plugin-reload-fanout.test.ts`).
  Локальный WIP добавил periodic exact-generation reconciliation живых
  зарегистрированных replicas: converged readiness пропускается, stale/not-ready
  replica получает повторный идемпотентный Reload, точный ACK записывается
  отдельно, expired lease не вызывается; повторяется только notification, не
  product operation. Покрыто
  `tests/integration/registered-replica-reconciliation.test.ts`.
  Открыты: решение о судьбе статического registry в `core.yaml` и drain.
  Истечение frozen lease или новая incarnation того же replica ID атомарно
  закрывает operation как `failed/target_lost`, без подмены target и изменения
  desired `active`; продолжение требует новой явной операции. Проверено
  `tests/integration/registered-rollout-target-lost.test.ts`; полный hosted gate
  остаётся открыт. До синхронизации и публикации owner-контракта
  `liapoldus.github.io/public/spec/errors.json` canonical error-catalog test
  намеренно остаётся красным. Проверка совместимости
  release-контрактов подключена до формирования когорты; directory long-poll
  реализован и покрыт SDK/Core conformance. Для
  зарегистрированного instance частичная конфигурационная operation теперь
  остаётся `running`; после позднего exact-generation convergence та же durable
  operation переводится в `succeeded`. Повторный recovery ограничен
  текущим authenticated replica directory. `registered-rollout-operation`
  запускает два отдельных процесса поверх одной SQLite и проверяет recovery
  service после повторной регистрации, но не полный `core serve` startup.
  **Закрытый подблок:** Core v10 транзакционно сохраняет marker для instance,
  прошедшего self-registration, загружает его перед запуском lifecycle listener
  и не разрешает legacy static fallback после рестарта. TypeScript fixture
  заново открывает SQLite и directory, проверяя, что dynamic instance fenced,
  а обычный unregistered static instance остаётся доступен.
  **Полный child-process тест реального `core serve` от старта до новой
  регистрации добавлен** (`tests/fixtures/serve-self-registration/main.go` +
  `tests/integration/serve-self-registration.test.ts`): фикстура собирает Core,
  стартует `core serve` как отдельный процесс, регистрирует нейтральную replica
  по mTLS, аутентифицирует peer-directory poll, останавливает Core, читает
  durable marker из SQLite, рестартует, подтверждает обязательную повторную
  регистрацию и отсутствие marker у static instance. Полный
  `make check` на этом WIP не прошёл: `core/go.mod` требует опубликованный SDK
  `v1.0.0`, а в `v1.0.1` изменён только TODO; код тега совпадает с `v1.0.0`.
  Read-only сверка также подтвердила, что remote `plugin-sdk/main` совпадает с
  локальным HEAD `a73df7e`, но не содержит dirty/untracked registration и
  peer-directory API, которые импортирует Core. Чистый
  `GOWORK=off go build ./...` падает на отсутствующих
  `ReplicaRegistrationRequest`, `ReplicaLifecycleContract` и
  `PeerDirectoryPollContract`. Targeted integration/build/vet/staticcheck
  проходили только с временным local Go workspace Core+SDK и не доказывают
  release build. Нужны завершённый SDK owner slice, его совместимый
  опубликованный immutable revision и точный Core pin; `GOWORK=off` process
  fixtures остаются заблокированы до этого шага.
  **Закрытый подблок в локальном WIP:** Core v11 атомарно продвигает generation
  вместе с immutable target cohort `(replicaId, incarnation, release digest)`;
  точные ACK сохраняются на target row, recovery не добавляет поздние
  incarnation и не переигрывает уже подтверждённые targets. В одной SQLite
  transaction `CreateCandidate` запрещает второй rollout; `RestorePrevious`
  блокируется до изменения pointers при открытом rollout или staging candidate.
  Rollback handler сериализует pointer swap + legacy Reload с `Apply` тем же
  application lock. Проверяется
  `registered-rollout-cohort.test.ts`, `plugin-settings-rollback.test.ts` и
  exact-dispatch child fixture.
  Membership зафиксирован: `immediate` включает каждую совместимую
  authenticated registration с действующей mTLS identity и неистёкшей lease,
  даже если replica ещё `ready=false`; readiness управляет traffic admission,
  а не первой доставкой конфигурации. Новые incarnation после snapshot не
  меняют frozen cohort. SDK generic contract compatibility подключена перед
  формированием config target cohort: одинаковый release digest совместим,
  разные digest требуют взаимного принятия всех advertised contract versions;
  нет evidence — promotion блокируется. `tests/integration/registered-release-cohort.test.ts`
  проходит 1/1. `tests/integration/registered-release-cohort-activation.test.ts`
  запускает настоящий Core configuration service с SQLite и доказывает, что
  отказ compatibility gate оставляет active/previous raw JSON и поколения без
  изменений, очищает staging, фиксирует operation как failed/conflict и не
  вызывает plugin apply. Lifecycle admission, renewal и deregistration
  сериализованы с cohort snapshot/promotion общим activation lock;
  `tests/integration/plugin-replica-directory.test.ts` проверяет, что lifecycle
  handler не публикует replica внутри activation critical section. Lock сейчас
  удерживается до окончания Reload fan-out; стоимость для частоты lease
  renewal нужно измерить на нагрузочном profile. Product-owned claims
  settings/durable-state остаются открытыми.
  Registered `PluginSettingsRollback` теперь в одной SQLite-транзакции меняет
  active/previous и записывает frozen exact-incarnation/release cohort; partial
  ACK оставляет operation `running`, recovery повторяет только unacknowledged
  targets и завершает operation после ACK всей когорты. Crash после pointer
  swap до перехода operation в `running` также восстанавливается; replacement
  incarnation не принимается. Red→green: `tests/integration/registered-rollout-cohort.test.ts`
  плюс `plugin-settings-rollback.test.ts`. Проверки ниже подтверждают этот
  rollback barrier, но совместимость поколений конфигурации для staged/canary
  rollout, drain и traffic rollout остаются открытыми.
- [x] Хранить в SQLite rollout intent/operations/audit, но не live endpoints;
  live endpoints публиковать через SDK REST long-poll. Core membership/policy
  changes и релевантное истечение lease будят watcher; scale event не создаёт
  config generation, reconnect возвращает актуальный snapshot и не теряет fence.
  Покрыто `tests/integration/peer-directory-poll.test.ts` и SDK/Core
  `peer-directory-conformance`; release/config rollout остаётся отдельной задачей.
- [ ] Реализовать отдельную platform-admin операцию
  `POST /api/plugins/{id}/rollouts`: точные candidate JSON bytes, desired
  release digest, expected active revision, stages и frozen candidate targets
  `replicaId + incarnation`. До изменения состояния проверить readiness/lease,
  candidate release digest, generic config schema и contract compatibility;
  все остальные participating replicas должны образовывать одну совместимую
  incumbent cohort. Зафиксировать обе когорты, intent, stage plan, operation и
  audit в SQLite; при начале roll-forward атомарно promoted candidate становится
  `active`, старый `active` — `previous`, `staging` очищается. Обычный
  `PUT /api/plugins/{id}/settings` остаётся immediate и не создаёт traffic
  rollout. Одновременный открытый rollout на instance запрещён.
  Уже реализованы multipart `POST /api/plugins/{id}/rollouts`, чтение состояния
  через `GET /api/plugins/{id}/rollouts/{rolloutId}` с strong ETag и отдельный
  platform-admin approval endpoint. Approval требует `If-Match` и
  `Idempotency-Key`; фиксация операции, stage transition, audit и idempotency
  выполняются одной SQLite-транзакцией. Stage IDs ограничены безопасным URL
  сегментом. Первая controller confirmation переводит исходную operation из
  `pending` в `running`; одобрение последнего stage завершает её. Это покрыто
  integration fixture с настоящим Management API и SQLite.
  Локальный подготовительный срез добавил строгий parser stage plan с проверкой
  точной уникальной incarnation на `replicaId`, монотонного роста весов до 100%,
  уникальных stage IDs, unknown fields, trailing JSON и duplicate JSON keys.
  Чистый selector затем сопоставляет candidate только с точной зарегистрированной
  incarnation, проверяет lease/readiness/release digest, выделяет одну
  incumbent release cohort и отклоняет частичную ступень без incumbent.
  Добавлены SQLite schema v14 и отдельный durable store для immutable plan,
  stages и точных candidate/incumbent `replicaId + incarnation`; сырой JSON plan
  сохраняется и восстанавливается без remarshal. Store отклоняет второй открытый
  rollout на instance. Транзакции `ConfirmStage` и `ApproveStage` проверяют
  точный вес, expected revision и observation window, двигают active stage,
  завершают operation и пишут audit атомарно; child-process test доказывает
  отклонение неправильного веса, premature/stale approval, продвижение и финальное
  завершение. Проверки: `traffic-rollout-plan.test.ts`,
  `traffic-rollout-storage.test.ts`, `traffic-rollout-store.test.ts`.
  Добавлен storage transaction primitive `CreateAndPromote`: в одной SQLite
  транзакции резервируются operation/idempotency, сохраняется точный JSON
  generation, двигаются active/previous, создаются config rollout ACK targets
  только для candidate cohort, traffic intent/stages/cohorts (включая
  incumbent) и creation audit. Incumbent сохраняет уже применённую runtime-
  конфигурацию и не получает candidate `Reload`. Child-process тест проверяет
  сохранение raw bytes, атомарный состав записей, exact candidate/incumbent и
  поведение повторного ключа/другого запроса; проверка дополнительно фиксирует,
  что config rollout target — только точный candidate replica. После изменения
  прошли шесть целевых rollout suites (13 тестов), целевой `go vet` трёх
  затронутых пакетов и `git diff --check`. Application worker читает
  незакрытые traffic/config rollout-ы из SQLite, передаёт candidate только
  замороженным `replicaId + incarnation`, сохраняет ACK каждой цели и при
  повторе вызывает только неподтвердившиеся targets. После рестарта worker
  повторно поднимает незакрытый barrier; потерянная точная incarnation закрывает
  barrier как `target_lost` без замены. Bootstrap запускает worker после создания
  intent, при регистрации replica и в периодическом recovery цикле. Child-process
  проверяет частичный ACK, повтор только для оставшейся candidate и отсутствие
  вызова incumbent. Targeted проверка с локальным workspace Plugin SDK: восемь
  rollout suites (11 тестов), четыре смежные config/reload suites, `go build
  ./...`, `go vet` четырёх затронутых пакетов и `git diff --check` прошли;
  published Core/SDK release gate остаётся открытым. Пока последний traffic
  rollout не завершён, обычные settings mutation/rollback и generic Reload
  fanout не меняют замороженные когорты. `target_lost` атомарно завершает
  traffic rollout как failed и сохраняет cohort fence; восстановление требует
  новой явной rollout-операции. Это покрывает
  `tests/integration/traffic-rollout-reload.test.ts`.
  Следующий application-срез добавил `TrafficRolloutService`: он проверяет
  plugin-owned schema через порт, разбирает строгий plan, разрешает только
  текущие точные candidate incarnation, фиксирует ready incumbent cohort и
  передаёт одну атомарную submission store под общим lifecycle/activation lock.
  Child-process test проверяет replay по idempotency key, отказ для неверной
  incarnation и незарегистрированной candidate replica. Добавлен generic JSON
  Schema validator для opaque plugin documents и адаптер release-cohort
  проверки к Plugin SDK; API создания уже подключён к bootstrap и
  протестирован. Для каждого этапа ручное одобрение обязательно: пропущенный
  `requireManualApproval` имеет default `true`, явный `false` отклоняется;
  parser и storage validator применяют одинаковую семантику, сохраняя исходный
  Plan JSON. Реализованы отдельный private mTLS listener из
  `core serve --traffic-controller-config`, собственные trust roots/CRL,
  exact identity allow-list, intent GET с ETag и conditional GET, скрытие intent
  до закрытия `plugin_rollouts.open`, confirmation с per-rollout CAS и
  identity-bound idempotency. Receipt, переход stage и audit фиксируются одной
  SQLite-транзакцией. Child-process tests проверяют реальный listener, отказ
  без client certificate, отказ неразрешённой identity, повтор confirmation и
  конфликт повторного ключа. Controller не может изменить plan или заменить
  обязательное ручное platform-admin approval.
- [x] Закрепить отдельную строгую v2-схему `traffic-controller.schema.json` и
  field-map; `LoadTrafficController` проверяет schemaVersion, неизвестные и
  повторные YAML-ключи, exact identity allow-list, bind и внешние file refs.
  TS child-process проверка покрывает валидный документ, duplicate identity,
  inline key и ошибки схемы. Минимальная v1 bootstrap-схема не расширялась.
- [ ] Завершить traffic-controller conformance: отозванный сертификат и CRL
  rotation, полный privilege-escalation negative matrix, pause при health/error/
  неподтверждённом весе, crash/restart между intent и confirmation, восстановление
  сохранённого idempotency receipt, а также drain долгих вызовов и открытых
  streams. Операционный acceptance должен подтвердить отсутствие автоматического
  rollback/replay и продолжение только с последнего подтверждённого stage.

### 2. Peer links и смешанное размещение — владелец Core + Plugin SDK

> **Resolved (canonical error contract):** `plugin_link_conflict` (409) и
> `plugin_link_invalid` (422) добавлены в `assets/contracts/errors.json`
> (+ mirror/manifest) и в канонический `liapoldus.github.io public/spec/errors.json`
> (+ hash в `public/spec/manifest.json`); docs `main` закоммичен (`fd9a61b`).
> `management-error-catalog.test.ts` зелёный.

- [x] Владелец durable source — SQLite Core; static YAML/assets не являются
  источником значений policy. Добавить Management API для списка и полного
  чтения/замены/удаления политики logical-пары caller/target. Ресурс имеет
  revision/ETag; create выполняется `POST` и конфликтует при существующей
  паре, replace/delete требуют `If-Match`; мутации требуют `Idempotency-Key`, валидируются,
  сравниваются по CAS и аудируются в транзакции. Отсутствие записи — deny;
  неизвестные пока instance IDs допустимы для предварительного authoring.
- [x] SQLite policy хранит набор правил пары: placement (`same-placement` или
  `remote`), совместимый carrier, weight `1..100` и opaque required contract
  ranges. Core проверяет только generic синтаксис/placement-carrier pairing,
  не разбирает продуктовые методы и schemas. После commit заменить immutable
  in-memory snapshot и уведомить SDK-defined directory watchers; при старте
  восстановить snapshot из SQLite.
- [x] Завершить `GET /internal/v2/plugin-peer-directory` строго по SDK owner
  контракту: mTLS-authenticated caller получает только разрешённые links; watch
  просыпается при policy/membership/readiness change и ближайшем релевантном
  lease expiry. Не добавлять периодический reaper только ради long-poll expiry:
  handler может ждать сигнал либо ближайший deadline. Не дублировать DTO/таймауты
  SDK в Core.
- [x] SDK resolver выбирает replica по routing key и weight, не импортируя
  `pluginprotocol`. Cross-component gate (`peer-directory-conformance`)
  прогоняет реальный Core handler через реальный mTLS TCP и SDK
  `PeerDirectoryClient`/`ResolvePeer`; покрытие: create/replace/delete CAS и
  audit (`plugin-links-api`), crash recovery (`link-policy-snapshot`), watcher
  notification, caller isolation, expiry, socket-only без TCP/QUIC fallback и
  отсутствие peer payload в Core (architecture tests). Gate исполняется
  in-process (реальная серверная реализация + реальный SDK клиент), без
  выделенного OS child-process Core v2 binary.

### 3. Остальные платформенные возможности v2

- [ ] Закрыть generic lifecycle/rollout gates с нейтральными child-process
  fixtures и product compatibility для Domain/Runtime. Server/forms-db не
  изменяются в v2; существующие v1 сценарии допускаются только как regression
  targets.

## V3 — отдельный этап, не включать в v2 gates

- [ ] Возобновить Server/forms-db repositories: Server multi-replica
  registration, release compatibility, shared storage/ACME и Caddy-L4; forms-db
  SQL cohort compatibility на PostgreSQL/MySQL/MariaDB.
  Product contracts остаются у соответствующих plugins; Core остаётся generic.

- [ ] Публичный Go host API Core, static composition root и Plugin SDK
  in-process adapter для доверенных Go plugins; REST+mTLS остаётся для
  отдельных процессов, без автоматического fallback.
- [ ] Перенести в Server Caddy-L4 и public TCP/UDP relay; P2P/NAT traversal
  не включать без отдельного решения.
- [ ] Identity/CAPTCHA — только после явной разморозки их repositories;
  продуктовые контракты остаются plugin-owned.
- [ ] Website/content развитие forms-db совместно с Server; Core хранит только
  opaque configuration, без content model. Подробный проектный backlog остаётся
  в forms-db TODO и v3 owner prompt.
- [ ] `pluginprotocol` C ABI и Python `cffi` bindings поверх существующей Go
  реализации; не создавать второй wire/session engine.
Установка, deployment и плановые обновления Core/plugins не входят ни в один
этап Core roadmap. Инструменты размещения не публикуют product JSON и не меняют
поколения конфигурации. Core хранит desired config после исчезновения workload; lease
ограждает старую incarnation. Release digest, сообщённый replica, служит для
проверки совместимости когорты, а не доказывает происхождение бинарника.
