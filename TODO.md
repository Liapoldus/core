# TODO — Core v1

Нормативный scope: [целевая архитектура](https://liapoldus.github.io/core/architecture/target), [Control Plane](https://liapoldus.github.io/core/architecture/control-plane), [ручной запуск v1 / автоматизация v2](https://liapoldus.github.io/core/architecture/plugin-deployment), [roadmap](https://liapoldus.github.io/core/architecture/v1-migration-roadmap) и [acceptance](https://liapoldus.github.io/core/configuration/acceptance).

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
- Plugin configuration — точные raw JSON bytes. В SQLite хранятся только два
  durable поколения `active` и `previous`; candidate полностью проверяется до
  транзакции и не создаёт третьего staging slot. Core не decode/remarshal-ит и
  не знает product fields.
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

- [ ] Завершить generic `PUT /api/plugins/{id}/config`: принимать сам JSON
  object без wrapper, сохранять точные UTF-8 request bytes, вычислять SHA-256 по
  ним же; проверять size, syntax, root type, duplicate keys и plugin-owned JSON
  Schema без изменения bytes.
- [ ] Свести конфигурационное durable storage к `plugin_config_generations` с
  колонками `instance_id`, `generation`, `slot`, `raw_json BLOB`, `sha256`,
  `schema_version`, `created_at`; разрешить только `active`/`previous`, по одной
  строке на слот. Удалить старые config stores после replacement/recovery tests.
- [ ] Реализовать CAS, `Idempotency-Key`, durable operations и аудит для
  settings, rollback, endpoint membership, interaction policies и plugin Admin
  Surface. Candidate validation failure не меняет slots/snapshot и не вызывает
  Reload.
- [ ] Реализовать atomic `active`/`previous` update до Reload, per-replica ACK,
  roll-forward/fencing, explicit rollback и crash recovery. Не replay-ить
  неизвестный plugin call outcome.
- [ ] Построить immutable in-memory snapshot из SQLite до readiness; runtime
  request path не читает SQLite или конфигурационные файлы.
- [ ] Завершить SQLite backup/restore, integrity/foreign-key checks,
  migrations и source-owned embedded SQL. Не добавлять PostgreSQL или S3.

## 2. Plugin SDK REST integration

- [ ] Завершить Core REST client/server adapters: fixed endpoint для каждой
  replica, per-replica mTLS, Manifest/schema/health/readiness, `Reload`, exact
  config pull и digest-bound ACK.
- [ ] Подключить SDK к реальному Core composition root и удалить Core lifecycle
  dependency/wiring через `pluginprotocol` после миграции активных consumers.
- [ ] Проверить reconnect после ручного operator restart: новый handshake,
  identity/schema/generation validation и reload при расхождении; никакого
  process launch, restart или replay со стороны Core.
- [ ] Проверить redaction, Management authorization, service credential
  rotation/revocation, scoped secret grants, audit и разделённые trust roots.

## 3. Plugin registration и product integration

- [ ] Довести Management API для CRUD generic plugin instance/replica fixed
  endpoints и ожидаемых identities; исключить process/container lifecycle,
  artifact catalog и provider management surfaces.
- [ ] Интегрировать вручную запускаемый `plugins/server`: конфигурация,
  SDK REST Reload/pull, plugin-owned schema/runtime apply, digest ACK и
  обработанный HTTP/TLS traffic. Имена в публичных API/fixtures — `server`, не
  `caddy`.
- [ ] Удалить Caddy-L4 dependency/registration и public TCP/UDP listener/relay
  implementation из v1 build; держать их только в v2 backlog.
- [ ] Интегрировать вручную запускаемый forms-db plugin: собственные schemas,
  REST lifecycle, persistent product data и Admin Surface.
- [ ] Не добавлять plugin-name/capability branches в Core. Не мигрировать и не
  собирать как Core v1 CAPTCHA/Identity.

## 4. Проверки и удаление старого lifecycle

- [ ] Обновить TypeScript integration/E2E в `tests/`: два config slots,
  byte-exact PUT→SQLite→pull, CAS/idempotency, Reload/ACK, manual restart,
  fencing/recovery, authorization/audit/redaction и Core→SDK→Server/forms-db.
- [ ] Удалить старые Core process supervision, provider/TUF installation API,
  SQLite provider/workload state, неиспользуемые CLI commands и legacy protocol
  lifecycle только после replacement tests и consumer audit.
- [ ] Синхронизировать versioned contracts/mirrors, OpenAPI, docs, this TODO и
  `AGENTS.md`; не оставлять конкурирующие lifecycle APIs.
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
- Обязательный порядок при выпуске новой конфигурации: старый `active`
  переносится в `staging`, валидированный кандидат становится `active`,
  затем `staging` становится `previous`. Прежний `previous` отбрасывается.
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
      Ошибка `'n'` вместо `422` присутствует только в опубликованных docs.

### План миграции потребителей (не выполняется в этом инкременте)

По решению владельца `plugins/server` и `plugins/forms-db` в этом инкременте
**не мигрируются**. Ниже — порядок будущей миграции, который следует из
финальной архитектуры v1.

1. Обновить зависимость на текущий standalone Plugin SDK; удалить
   `pluginprotocol`-импорты и собственный lifecycle-код.
2. Заменить собственный config source на `NewCoreConfigurationSource`:
   приложение больше не читает настройки из env, argv или конфиг-файлов.
3. Реализовать `Reload(generation)` через SDK: подтвердить только после
   успешного применения, иначе вернуть ошибку и оставить старую генерацию.
4. Pull-ить **точную** запрошенную генерацию; не применять «последнюю
   известную» и не кэшировать конфигурацию на диске.
5. Удалить собственные `/reload`, `/config` и admin-поверхности, которые уже
   публикует Plugin SDK, чтобы не осталось двух lifecycle API.
6. Снять v1-специфичные остатки: TUF release installation, container
   providers, artifact/execution/pluginCatalog поля.
7. Проверить приёмку: `Management PUT -> SQLite -> Reload -> exact-generation
   pull -> ACK` для обоих потребителей.
8. Отдельно: владелец `liapoldus.github.io` синхронизирует
   `public/spec/management.openapi.yaml` (маршрут rollback, числовые коды 422)
   и error catalog; Core эти файлы не редактирует.

### Внешние расхождения (вне владения Core)

- Канонический `core.schema.json` в docs-repo требует
  `["state","management"]` и не содержит `pluginControl`, тогда как Core
  объявляет `["state","management","pluginControl"]`. Закрывает владелец
  `liapoldus.github.io`.
- Опубликованный management spec не содержит маршрута
  `/api/plugins/{pluginId}/rollback`, хотя Core его реализует; в Core mirror
  маршрут добавлен. Синхронизацию docs выполняет их владелец.
- Опубликованные docs содержат `'n'` вместо числового кода `422` в enum
  состояний операции. В Core mirror значение корректно (`recovering`).

