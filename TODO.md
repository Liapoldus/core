# TODO — Core v1

Нормативный scope: [целевая архитектура](https://liapoldus.github.io/core/architecture/target), [Control Plane](https://liapoldus.github.io/core/architecture/control-plane), [ручной запуск v1 / автоматизация v2](https://liapoldus.github.io/core/architecture/plugin-deployment), [roadmap](https://liapoldus.github.io/core/architecture/v1-migration-roadmap) и [acceptance](https://liapoldus.github.io/core/configuration/acceptance).

## Документация

- [x] Канонические Core Markdown и Mermaid исходники принадлежат этому repo в
  `docs/site/core/` и `docs/site/diagrams/`. Агрегатор `liapoldus.github.io`
  собирает закреплённый commit SHA в единый сайт без редактируемой копии.
- [ ] После изменения Core docs синхронизировать pinned SHA в
  `liapoldus.github.io/docs-sources.json` и выполнить агрегаторный build.

## Зафиксированная граница v1

- Состав v1: три сервиса — Core, Server plugin, forms-db plugin; две библиотеки —
  Plugin SDK и `pluginprotocol`. CAPTCHA и Identity отложены до v2 и заморожены.
- Оператор отдельно устанавливает и вручную запускает Core и оба plugin
  binaries. Core не устанавливает, не запускает, не останавливает, не
  перезапускает, не масштабирует и не удаляет plugin processes или containers.
- Core подключается только к явно зарегистрированным fixed endpoints, проверяет
  per-replica mTLS identity, Manifest/schema и health/readiness. Он публикует
  exact-generation config endpoint для SDK REST pull; сам Core только вызывает
  `Reload(generation)` и не передаёт settings payload в notification.
- Docker/Compose, Swarm, Kubernetes, provider API, local process supervision,
  TUF package installation и plugin workload lifecycle — v2. Не включать их в
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
- Constructor и `react-lib` заморожены. Не менять их, CAPTCHA и Identity.
- Server plugin v1 — HTTP/HTTPS only. Caddy-L4 и public TCP/UDP relay не
  входят в binary, settings schema, API, tests или acceptance.

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
- [ ] Построить immutable in-memory snapshot из SQLite до readiness; runtime
  request path не читает SQLite или конфигурационные файлы.
  **ПРОВЕРЕНО 2026-09-30: НЕ РЕАЛИЗОВАНО.** Слово `snapshot` не встречается ни в
  одном файле `internal/`; readiness вычисляется из таблицы наблюдений
  `plugin_replicas` на каждый запрос. Это реальный пробел v1, а не
  формальность: runtime path ходит в SQLite.
- [ ] Завершить SQLite backup/restore, integrity/foreign-key checks,
  migrations и source-owned embedded SQL. Не добавлять PostgreSQL или S3.
  **ПРОВЕРЕНО 2026-09-30: частично.** Embedded source-owned SQL, `schema_migrations`
  и проверка версии есть; апгрейд v6→v7 идёт идемпотентным применением схемы
  (`sqlite.go:64` допускает меньшую версию, `sqlite-version-guard` это
  закрепляет). Отсутствуют `integrity_check`/`quick_check` и backup/restore —
  их в `internal/` и `assets/` нет вообще.

## 2. Plugin SDK REST integration

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
  Надёжный startup (config + pluginControl trust material) и достижимость каждой
  объявленной реплики теперь являются условием старта: `core serve` падает, а не
  поднимается «наполовину». Покрыто:
  `tests/integration/management-api-characterization.test.ts` (drift: true без
  evidence), `tests/architecture/presentation-boundaries.test.ts`,
  `tests/architecture/v1-migration-contracts.test.ts`.
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
- [ ] Проверить reconnect после ручного operator restart: новый handshake,
  identity/schema/generation validation и reload при расхождении; никакого
  process launch, restart или replay со стороны Core.
- [ ] Проверить redaction, Management authorization, workload credential
  rotation/revocation, scoped secret grants, audit и разделённые trust roots.
- [ ] Реализовать Core-side SDK secret-grant issue/redemption endpoints. Сейчас
  их нет в production router/storage, хотя SDK contract и Core schema обещают
  scoped Core REST grants. Это v1-блокер для plugin-конфигураций с opaque secret
  references (включая persistent storage forms-db и custom TLS server plugin).
  Грант должен быть one-use и scoped к аутентифицированной replica, точному
  active generation, reference и purpose; никакого plugin-to-plugin interaction
  authorization этим API не предоставляется.

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
  ВНИМАНИЕ: физические колонки `plugin_instances.mode` и `plugin_instances.endpoint`
  пока остались в схеме (см. «Требует решения владельца» про безопасную миграцию
  parent-таблицы). Новые регистрации их не заполняют, inventory-запрос их не
  читает, но формально колонки ещё существуют.
- [ ] ~~Довести Management API для CRUD generic plugin instance/replica fixed
  endpoints и ожидаемых identities~~ **Снято решением A:** объявление topology
  перенесено в `core.yaml`, поэтому Management API регистрации не появился и
  добавлять его не нужно. OpenAPI/docs локально синхронизированы с этим
  решением: registration/mutation, interaction и cookie-policy paths удалены;
  остаются только читаемые plugin instances, settings, rollback, Admin Surface,
  operations, audit и access. Docs commit `453b15e` опубликован в `main`;
  Pages deployment после этого commit нужно проверить отдельно.
- [ ] Завершить интеграцию вручную запускаемого `plugins/server`: текущий
  WIP содержит SDK REST adapter и строгую settings/runtime реализацию, но
  `GOWORK=off go test ./...` на 2026-09-30 падает: production-пакеты всё ещё
  импортируют удалённые `pluginprotocol/pluginv1` и `presentation/sdk`, а REST
  adapter не совпадает с текущим публичным API Plugin SDK. До зелёной сборки и
  Core→SDK→Server smoke интеграция не завершена. Публичное имя — `server`.
- [ ] Удалить Caddy-L4 dependency/registration и public TCP/UDP listener/relay
  implementation из v1 build; держать их только в v2 backlog.
- [ ] Завершить интеграцию вручную запускаемого forms-db: его текущая сборка
  (`GOWORK=off go test ./...`, 2026-09-30) падает из-за оставшихся импортов
  `pluginprotocol/pluginv1` и `presentation/sdk`. Сохранить plugin-owned schema,
  persistent product data и Admin Surface при переносе lifecycle на SDK.
- [ ] Не добавлять plugin-name/capability branches в Core. Не мигрировать и не
  собирать как Core v1 CAPTCHA/Identity.

## 4. Проверки и удаление старого lifecycle

- [ ] Обновить TypeScript integration/E2E в `tests/`: два config slots,
  byte-exact PUT→SQLite→pull, CAS/idempotency, Reload/ACK, manual restart,
  fencing/recovery, authorization/audit/redaction и Core→SDK→Server/forms-db.
- [x] Удалить старые Core process supervision, provider/TUF installation API,
  SQLite provider/workload state, неиспользуемые CLI commands и legacy protocol
  lifecycle. Регрессии закреплены `pluginprotocol-sdk-boundary`,
  `no-legacy-site-api`, `target-cli-surface`, `dead-artifacts`.
- [ ] Синхронизировать versioned contracts/mirrors, OpenAPI, docs, this TODO и
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
- [ ] Прогнать `make check`, `go vet ./...`, `make staticcheck-u1000`,
  `go build ./...`, macOS/Linux builds и smoke с отдельно вручную запущенными
  сервисами. Фиксировать только реально полученные результаты.

## Отложено до v2 — не включать в v1 gates

- [ ] Caddy-L4, public TCP/UDP listeners/relay.
- [ ] CAPTCHA и Identity/OIDC/OAuth; repositories остаются полностью frozen.
- [ ] TUF trust metadata/catalog и Core API/CLI для установки или обновления
  plugin binaries.
- [ ] Core-supervised local plugin processes, binary release install,
  inherited listener/bootstrap и автоматический restart/backoff.
- [ ] Docker/Compose, Swarm и Kubernetes providers, managed workloads,
  reconciliation, rollout/scale/drain и ownership-safe cleanup.
- [ ] Любые API, credentials, database fields, CLI commands и tests, которые
  существуют только для этих v2 features.

## Статус инкреста (preflight + миграция v1)

Зафиксировано 2026-09-30. Baseline до изменений: `go build ./...` PASS,
`go vet ./...` PASS, `npm --prefix tests test` — 13 failing / 97 passing.
Branch `main`, HEAD `e07cc35`; worktree намеренно содержит большой
pre-existing dirty/untracked changeset — не сбрасывать.

### Решения владельца (приняты, не пересматривать)

- Приватный listener exact-generation config pull
  (`GET /internal/v1/plugin-config/{generation}`) с отдельным от Management
  mTLS trust root остаётся в Core и объявляется блоком `pluginControl` в
  bootstrap schema. Канонический `core.schema.json` в docs-repo требует
  `["state","management"]` и не содержит `pluginControl` — это расхождение
  закрывается владельцем `liapoldus.github.io`, Core его не редактирует.
- Canonical docs для contract tests резолвятся из локального git-checkout
  только если `HEAD == origin/main`, иначе REST API, иначе тест падает.
  Молчаливый untracked fallback запрещён.

### Выполнено

- [x] `assets/contracts/core.schema.json`: удалены v2 `execution`, `artifacts`,
      `pluginCatalog`; `required` = `["state","management","pluginControl"]`,
      `additionalProperties: false`. `contracts/v1/**` пересобраны, digests
      пересчитаны.
- [x] `tests/architecture/v1-migration-contracts.test.ts` переписан под целевой
      v1 контракт.
- [x] `tests/support/canonical-contracts.ts`: verified-checkout резолвер.
- [x] `management-error-catalog.test.ts` переведён на резолвер, ассерты сохранены.
- [x] Единственный shared JSON response writer; устранён второй
      `json.NewEncoder` в `plugin_config_pull.go`.
- [x] Убран root-реэкспорт `PluginReplicaIdentityResolver` из
      `internal/presentation/api`.
- [x] Удалены `handlers.PluginRestart`, `PluginDependencies.RestartPlugin` и
      путь `restart` из `management-fields.yaml`.
- [x] `npx vitest run architecture/` — 31 file / 72 tests PASS.

### BLOCKER — SDK-модуль не компилируется (вне владения Core) — РАЗРЕШЁН

`core/go.mod` содержит `replace` на `../plugin-sdk`, из-за чего некомпилируемый
SDK блокировал `go build ./...` и все Go-backed integration tests. Владелец SDK
починил `infrastructure/plugin_client.go` и
`infrastructure/core_config_source.go` (`MutualTLSClient`, `Plugin.Endpoints`
как `map[string]Endpoint` с lookup-хелпером). Core использует только
`NewCoreConfigurationSource`, `NewPluginClient` и `PullExact`; второй
SDK-контракт не создавался. Приёмка
«Management PUT -> SQLite -> Reload -> exact-generation pull -> ACK»
выполнена (`tests/integration/plugin-sdk-config-pull.test.ts`).

### Решение владельца по `staging` (принято, не пересматривать)

- Durable `staging`-слот **сохраняется**. Он является внутренним рабочим
  буфером и никогда не публикуется наружу: replica может pull-ить только
  `active` и `previous`. Опубликованных durable config slots по-прежнему два.
- Обязательный порядок при выпуске новой конфигурации: после validation exact
  candidate bytes сохраняются в `staging` вместе с durable operation; до
  promotion прежние `active`/`previous` остаются неизменными. Promotion одной
  транзакцией удаляет старый `previous`, переносит прежний `active` в
  `previous`, а candidate из `staging` в `active`.
- При неуспешной validation `staging` не создаётся. При отказе после записи
  candidate recovery завершает roll-forward либо удаляет staging-candidate по
  durable operation state; plugin никогда не может pull-ить staging.
- `OperationPayload` хранит только digest, а не settings bytes, поэтому
  `staging` — load-bearing для recovery незавершённой операции: без него
  прерванный rollout нельзя восстановить.
- Slice C «убрать durable staging» **отменён**. Следствия ужесточены в
  `core/AGENTS.md` и workspace `AGENTS.md`.

### Осталось

- [x] Slice D: `pluginprotocol` и process supervision удалены из Core, из
      `go.mod`, fixtures, `Dockerfile` и `scripts/docker-smoke.sh`. Остаточный
      checkout `pluginprotocol` удалён также из `.github/workflows/verify.yml`;
      в Go-исходниках и `go.mod` ссылок нет.
- [x] Fixtures и tests, передававшие удалённые `artifacts`/`execution`/
      `pluginCatalog` в `core.yaml`, очищены; fixtures объявляют
      `pluginControl` под новую схему.
- [x] В write path settings body нет JSON-Schema валидации: `schemaVersion`
      переносится как непрозрачное целое и проверяется на согласованность, а
      мёртвая ветка `schemaViolation` удалена. Per `AGENTS.md` JSON Schema —
      разрешённая («may validate»), но не обязательная опция v1, поэтому
      product-специфичная валидация не добавляется.
- [x] v2-утечка из `contracts/v1/management.openapi.yaml` устранена: удалены
      `/install`, `/restart` и осиротевшая схема `PluginReleaseSelection`;
      описания `Conflict`/`Invalid` синхронизированы с каноническим spec
      (`plugin_mode_operation_forbidden`; без `plugin_catalog_untrusted`,
      `plugin_release_incompatible`). Добавлен отсутствовавший в обоих
      репозиториях маршрут `/api/plugins/{pluginId}/rollback`.
      Остаточные contract расхождения проверяются вместе с docs owner после
      синхронизации публичного spec; не добавлять исправленный текст в Core
      зеркало в обход его publication workflow.

### Потребители и межрепозиторная интеграция

Core REST composition и per-replica registration/observation wiring добавлены
в commit `dcc0a90`; Core `make check`, `go vet ./...` и
`make staticcheck-u1000` проходят. Это не доказывает готовность product
consumers: `plugins/server` и `plugins/forms-db` пока не компилируются против
удалённого protocol lifecycle и нового SDK. Активные интеграционные действия
закреплены в их repo-owned TODO. Не добавлять в Core fallback на legacy API.

### Найдено при введении race-гейта — не чинилось, нужно решение владельца

`PluginConfigurationService.Recover` пере-применяет операции в состоянии
`running`, а на уже settled-базе является no-op. В v1 это недостижимо:
`bootstrap.serveBootstrap` вызывает `Recover` ровно один раз до
`management.Listen`, когда ни одного apply ещё нет. Но защиты у этого
инварианта нет, и конкурентный вызов `Recover` даёт двойной apply.

Обнаружено race-фикстурой: 49 вызовов `ApplyConfiguration` на 48 успешных
операций. Обе цифры сходятся, если `Recover` вызывать только до начала
работы и на settled-базе, как в реальном bootstrap.

- [ ] Решить, нужен ли guard (например, запрет `Recover` при непустом
      `operationWorkers`, либо перевод recover-worker'а на тот же
      per-operation дедупликатор, что и `scheduleApply`). Поведение
      наблюдаемое, поэтому без решения владельца не меняю.
- [x] Race-гейт перестал быть вакуумным: `make test-race` больше не
      выполняет `go test -race ./...` (в проекте нет Go `*_test.go`, он
      компилировал пустоту и рапортовал успех). Теперь это
      `tests/integration/race-detection.test.ts`, который собирает сервис и
      фикстуры под `-race` с `GORACE=halt_on_error=1` и прогоняет 8 инстансов
      × 6 раундов конкурентных apply/read/recovery через реальный
      management handler. Гонка валидирована контрольным запуском: с
      внедрённым несинхронизированным доступом gate падает (exit 2,
      `DATA RACE`).

### Внешние контракты

Schema, OpenAPI, error catalog и manifest синхронизированы с docs commit
`ae2a734`; Core `make check` прошёл после синхронизации. Если меняется публичный
контракт, сначала меняется docs owner source, затем обновляется Core mirror и
его digest/vector gate. Новый docs build прошёл, но deployed `/core/` пока
отвечает `404`, а legacy `/gateway/` — `200`; доступность нового Pages build
остаётся внешним deployment gate.
