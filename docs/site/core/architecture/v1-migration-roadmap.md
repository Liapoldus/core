# План реализации Core v1

План переводит систему к [целевой архитектуре](target). Это актуальный backlog,
не история проверок; фактические acceptance gates перечислены в
[матрице v1](../configuration/acceptance). Репозиторные детали и задачи живут
только в TODO владельцев: [Core](https://github.com/Liapoldus/core/blob/main/TODO.md),
`plugin-sdk/TODO.md` (локальный модуль без remote),
[pluginprotocol](https://github.com/Liapoldus/pluginprotocol/blob/main/TODO.md)
и TODO конкретных plugins.

## Неизменяемые решения

- Core — один plugin-agnostic control plane и единственный writer SQLite.
- Configs — plugin-owned raw JSON objects. Management `PUT` принимает сам
  документ без envelope; Core сохраняет точные UTF-8 bytes как BLOB, считает
  SHA-256 по этим bytes и не decode/remarshal-ит документ.
- В SQLite для каждого instance ровно одна таблица поколений с колонками
  `instance_id`, `generation`, `slot`, `raw_json BLOB`, `sha256`,
  `schema_version`, `created_at`. Допустимы durable слоты `active`, `previous`
  и непубликуемый `staging`; validated candidate сохраняется там вместе с
  durable operation для recovery до promotion.
- Core проверяет syntax, UTF-8, size, дублирующиеся keys и generic JSON Schema.
  Он не интерпретирует product fields. Невалидный candidate не меняет active и
  не запускает Reload.
- Plugin SDK — независимый четырёхслойный Go module для Core↔plugin REST.
  `pluginprotocol` — независимая четырёхслойная generic plugin↔plugin library.
  SDK не импортирует protocol; Core использует SDK REST и не импортирует
  `pluginprotocol`.
- Для конфигурационного lifecycle Core вызывает `Reload(generation)`, а plugin
  pull-ит точную immutable generation. После validation Core одной транзакцией
  продвигает новый active и сохраняет бывший active как previous до fan-out;
  partial update идёт roll-forward с per-replica ACK и fencing. Rollback меняет
  active/previous местами и также выполняется как roll-forward.
- В v1 оператор вручную устанавливает и запускает Core, Server plugin и forms-db.
  Core подключается к fixed endpoints и не управляет plugin process/container
  lifecycle. Local process supervision, TUF installation и Docker/Compose/
  Swarm/Kubernetes providers полностью отложены до v2; для них не создаются v1
  API, SQLite state или acceptance gates.
  Constructor и `react-lib` заморожены. Caddy — отдельный plugin.

## Порядок реализации

### 1. Plugin SDK contract и каркас

Зафиксировать версионированные REST schemas, endpoint behavior, authentication,
limits, error model, generation/digest ACK, idempotency, retries, cancellation,
recovery и redaction. SDK предоставляет общий server/client, bootstrap,
Manifest/schema discovery, health/readiness, Reload, exact config pull,
Prometheus metrics и JSON stdout/stderr logging. Process shutdown/drain не
является Core↔plugin API в v1: его выполняет оператор средствами ОС.

Владелец: `plugin-sdk/`. Публичный endpoint contract и TS vectors должны
предшествовать consumer migration. Модуль остаётся локальным до решения о
canonical path/remote.

**Gate:** SDK contract tests, focused Go build/vet, реальные HTTP child-process
tests и четыре слоя без cross-layer imports.

### 2. Plugin-to-plugin protocol

Сохранить в `pluginprotocol/` только generic peer-to-peer registration, calls,
streams, carrier abstraction и transport security. Удалить lifecycle, config,
Manifest, Core grants и plugin product contracts только после переноса всех
потребителей и подтверждения тестами отсутствия ссылок. TCP/QUIC и security
выбираются независимо от application handlers.

**Gate:** TS conformance для разных carriers, user-registered methods,
cancellation/backpressure, identity/revocation и отсутствие Core/SDK/product
зависимостей.

### 3. Raw config store и Core REST lifecycle

В `core/` заменить отдельные revision/pointer/payload stores на одну таблицу
`plugin_config_generations`. Migration обязана сохранить активный конфиг,
проверить его digest/schema и создать максимум две строки на instance. Путь
GET config отдаёт только запрошенные instance, replica и generation. `PUT`
принимает напрямую документ, сохраняет исходный body и использует CAS плюс
idempotency headers. Durable operation ссылается на поколение и digest, не
дублирует body.

Затем подключить REST SDK client, per-replica mTLS, Reload fan-out, exact pull,
ACK, generation fencing, roll-forward, rollback, in-memory snapshot и crash
recovery. Сначала unit/integration TS tests в `core/tests/`, затем реализация.

**Gate:** exact-byte PUT→SQLite→pull round-trip; изменённый whitespace меняет
digest; duplicate keys и invalid schema не меняют slots; CAS/retry/rollback и
все crash boundaries проходят.

### 4. Ручное размещение плагинов и security

Реализовать регистрацию fixed endpoint и ожидаемой identity для каждой
replica. Оператор вручную запускает каждый plugin и отвечает за его process
lifecycle, обновление и persistent data. Core выполняет per-replica mTLS,
Manifest/schema/health checks, конфигурационный Reload/pull/ACK, Management API
authorization, scoped secret grants, audit и redaction. Plugin-to-plugin
interaction policy API и grants вынесены за v1 и не являются Core Management
API.
Core не получает process-control privileges, provider credentials или
контейнерные APIs.

**Gate:** отдельно запущенные Core/SDK/plugin проходят mTLS handshake, ручной
restart/reconnect, identity/revocation и config recovery tests без запуска или
перезапуска plugin со стороны Core.

### 5. Plugin migration

Последовательно перевести активные v1 repositories `plugins/server` и
`plugins/forms-db` на Plugin SDK и удалить повторные общие lifecycle endpoints
из них. Каждый активный plugin остаётся владельцем своего Manifest, settings
schema, capability, business error, admin surface и durable product data.
`plugins/captcha` и `plugins/identity` заморожены целиком, исключены из active
workspace и v1; не менять их исходники, тесты, contracts или зависимости и не
выполнять их миграцию/conformance до явной разморозки. Server plugin владеет
HTTP/HTTPS, ACME, HTTP sites/artifacts и `current`/`previous`; Caddy-L4 и public
TCP/UDP relay исключены из v1.

**Gate:** product contract and REST conformance для активных v1 plugins Server и
forms-db, followed by Core-to-plugin integration; никаких product-name
branches в Core или SDK.

### 6. Recovery и v1 acceptance

Завершить согласованные SQLite/config backup, operation recovery, Core
reconciliation и plugin volume runbooks. Прогнать все критерии в
[матрице acceptance](../configuration/acceptance), платформенные builds и
standalone smoke с вручную размещёнными сервисами. Удалить оставшийся legacy code/contracts после consumer/build/
documentation audit; не оставлять permanent compatibility layer.

**Gate:** каждый обязательный acceptance gate имеет актуальный воспроизводимый
PASS. Не объявлять Core v1 готовым при любом пропущенном или
неподтверждённом gate.

## Отложено до v2

Не создавать v1 API, SQLite state, dependencies или acceptance под Caddy-L4 и
public TCP/UDP relay; CAPTCHA/Identity; TUF, catalog и установку plugin
releases; Core-managed process supervision; Docker/Compose, Swarm или
Kubernetes providers; Unix domain socket и Windows named-pipe carriers в
`pluginprotocol`, а также Python SDK и pairwise Go↔Python conformance. Локальные
carriers требуют явного выбора, обязательного mTLS,
OS permissions/ACL только как дополнительной защиты и реальных platform-specific
tests. Межъязыковая реализация требует общей language-neutral wire спецификации,
исполняемых vectors и реальных Go↔Python child-process interop tests. Подробности
приведены в [v2-разделах протокола](protocol#межъязыковые-реализации-в-v2).
Подробная граница перечислена в [целевой архитектуре](target).

### Plugin SDK in-process adapter и единый бинарник

В v2 добавить in-process adapter для статически включённых доверенных Go
plugins. Он заменяет только Core↔plugin REST внутри одного процесса: Core
вызывает generic `Reload` interface, plugin сам pull-ит точное поколение через
scoped `ConfigSource`, реализованный над immutable Core snapshot. Семантика
generation/digest/ACK, rollback, grants, authorization, audit и errors остаётся
общей с REST adapter. В том же SDK сохранить REST+mTLS adapter для отдельных и
удалённых plugin processes; выбирать adapter явно на instance/composition, без
fallback. Один executable с дочерними plugin processes остаётся REST-режимом,
не in-process.

In-process компоненты — одна граница доверия и отказа; mTLS между ними
неприменим, panic containment не даёт process isolation. Поэтому в этот режим
включаются только доверенные compiled-in modules. `pluginprotocol` не меняется:
межплагинные вызовы остаются на его generic API с mTLS; прямые Go-вызовы и
каналы между plugins не допускаются.

**Gate:** SDK lifecycle conformance проходит одинаково через REST и in-process;
проверены pull exact generation, exact raw bytes/digest, ACK/retry/failure,
instance scoping, authorization/grants/audit и отмена. Smoke доказывает, что
in-process профиль не открывает Core↔plugin REST listener, REST-профиль
сохраняет mTLS, а plugin-to-plugin mTLS не изменяется.

## Правила агентной работы

Агенты могут выполнять независимые вертикальные slices параллельно, но у каждого
среза один файловый/контрактный owner. Разрешено удалять старую архитектуру,
публичные legacy surfaces и код без compatibility shim, если миграционный путь
и владельцы проверены, а replacement contract/tests готовы. Не удалять данные,
чужие dirty/untracked изменения, архивы или репозитории. Изменения проверяются
по AGENTS.md целевого репозитория; вся реализация тестируется по целевому
поведению, не по старой реализации.
