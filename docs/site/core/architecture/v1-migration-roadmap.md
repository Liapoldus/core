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
  lifecycle. Внешнее Docker/Swarm/Kubernetes размещение и саморегистрация
  относятся к v2; установку и плановые обновления выполняет оператор. Core process supervision не является целевым режимом. Для будущих этапов не создаются v1
  API, SQLite state или acceptance gates.
  Caddy — отдельный plugin.

## Порядок реализации

### 1. Plugin SDK contract и каркас

Зафиксировать версионированные REST schemas, endpoint behavior, authentication,
limits, error model, generation/digest ACK, idempotency, retries, cancellation,
recovery и redaction. SDK предоставляет общий server/client, bootstrap,
Manifest/schema discovery, health/readiness, Reload, exact config pull,
Prometheus metrics и JSON stdout/stderr logging. Process shutdown/drain не
является Core↔plugin API в v1: его выполняет оператор средствами ОС.

Владелец: `plugin-sdk/`. Публичный endpoint contract и TS vectors должны
предшествовать consumer migration. Canonical module path утверждён:
`github.com/Liapoldus/plugin-sdk`; coordinated `go.mod`/consumer migration
остаётся открытым.

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

## Production readiness v2 — межсубъектная приёмка

v2 считается готовой только как согласованный релиз экосистемы, а не по
отдельным зелёным сборкам. Контракты и доказательства принадлежат владельцам;
эта секция задаёт общие сквозные критерии и не дублирует product schemas.

- **Replica lifecycle:** Core переживает рестарт с пустой in-memory directory;
  новые incarnation регистрируются с уникальной mTLS identity, leases истекают
  по TTL, stale/подменённая replica fenced, а peer-directory не создаёт новую
  product config generation.
- **Rollout:** release digest/config generation, совместимость когорт,
  Reload/ACK, drain и внешние traffic weights работают как единая операция.
  Partial ACK и отказ traffic controller останавливают продвижение на последнем
  подтверждённом состоянии; неизвестный результат не replay-ится. Для
  неподтверждённой или несовместимой когорты нет нового трафика.
- **Placement и carriers:** вручную либо внешним Docker/Swarm/Kubernetes
  operator-ом запущенные workloads регистрируются без Core orchestration API.
  TCP/QUIC/Unix socket/Windows named pipe выбираются явно; mTLS, identity,
  revocation и отсутствие fallback проверены на каждой заявленной платформе.
- **Peer-link policy:** правила caller instance → target instance создаются и
  изменяются только через Core Management API, хранятся durable в SQLite с
  CAS/ETag и audit, а эффективная политика публикуется через SDK-defined
  long-poll directory. Проверены policy update/remove, deny-by-default,
  caller-scoping, directory wake-up и немедленное исключение replica после
  lease expiry без отдельного фонового reaper.
- **Server и forms-db:** их текущая v1 реализация остаётся regression baseline;
  Server-specific scaling/storage/ACME conformance и forms-db multi-replica
  SQL/cohort compatibility перенесены в v3. В v2 Core/SDK rollout и product
  compatibility проверяются на нейтральных fixtures; эти product repositories
  не изменяются.
- **Domain и Runtime:** минимум три Domain voting nodes проходят leader failover,
  quorum loss, fresh-read barriers, bounded snapshots и согласованную model
  migration/rollback. Runtime проходит WASM sandbox/limits, artifact integrity,
  compatibility rollout и Domain authorization/fencing без cross-plugin ACID
  обещаний. Runtime-side HTTP terminal adapter использует только неизменённый
  v1 Server contract; изменение Server repository или переход на новую Server
  API входит в v3.
- **Сквозной и эксплуатационный gate:** clean-environment сценарий запускает один
  Core и отдельно размещённые плагины; проверяет restart/reconnect, конфигурацию,
  прямые разрешённые peer-вызовы, mTLS/CRL rotation, redaction, recovery,
  generic rollout, Domain failover, Runtime peer/WASM и отсутствие payload в
  Core. Каждый заявленный OS/carrier и Domain storage profile исполняет native gate;
  cross-build или schema-only test не заменяет runtime evidence.
- **Согласованный релиз:** все owner suites, Core/SDK/protocol gates, VitePress,
  cross-repository E2E и deployment walkthrough имеют воспроизводимый PASS;
  owner TODO содержит точные команды/окружения и открытых blockers нет. До
  прохождения последнего пункта система не называется production-ready v2.

Если для конкретного deployment или filesystem profile нет среды, это остаётся
непройденным gate с названием отсутствующего профиля/доказательства, а не
условным PASS. Полный план лежит в workspace `tasks/`, а BigPickle dispatch
prompts — в `bigpikle/`; эти каталоги не публикуются VitePress и не заменяют
нормативные owner TODO.

## Будущие этапы — вне v1

Не создавать v1 API, SQLite state, dependencies или acceptance под функции
будущих этапов. v2 охватывает self-registration/rollout, внешнее размещение,
смешанные peer transports, Domain и Runtime. Server/forms-db repositories не
меняются в v2; их v1 paths — только regression targets. Studio развивается
отдельно и не входит в текущий v2 план. Caddy-L4/public TCP/UDP, CAPTCHA/Identity,
Python FFI binding поверх C ABI, Core embedding API, Plugin SDK in-process/static
composition, все дальнейшие Server/forms-db product changes (включая Server
scaling/shared storage и forms-db multi-replica SQL compatibility/website content),
отложены до v3. Установку и плановые обновления Core/plugins выполняет оператор
выбранными средствами; Core не получает provider
API ни в одном этапе. Identity и CAPTCHA заморожены до
отдельной явной разморозки. `pluginprotocol` остаётся
единственной Go wire/session реализацией; второй Python engine не создаётся.
Native artifacts и Python `cffi` wheels проверяются на Linux amd64/arm64, macOS
arm64 и Windows amd64. Подробности и ABI/interop gates приведены в
[C ABI дизайне](protocol#межъязыковой-доступ-через-c-abi-в-v3); Core и SDK
composition — в [целевой архитектуре](target).

### V3: Plugin SDK in-process adapter и единый бинарник

В v3 добавить in-process adapter для статически включённых доверенных Go
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

### V3: Core как Go library и статическая композиция

Core предоставляет публичный host API, позволяющий Go-приложению создать,
запустить, проверить readiness и остановить один Core runtime. API использует
штатную composition root и bootstrap/config source; не раскрывает `internal/`
типы, не обходит SQLite и не создаёт параллельную модель конфигурации. Core
остаётся singleton относительно своей state database.

Отдельный сборочный composition root включает Core и выбранные plugin factories
в один executable. Встроенные Go plugins подключаются через Plugin SDK
in-process adapter; REST adapter остаётся для отдельно запущенных и удалённых
plugins. Состав фиксируется на build time; dynamic Go plugin loading,
независимое обновление встроенного plugin без пересборки и обещание process
isolation не входят в scope. Упаковка executable с child processes сама по себе
не переводит их на in-process transport.

**Gate:** host API smoke создаёт/закрывает один Core и освобождает ресурсы;
composed binary содержит только явно включённые factories; in-process plugin
проходит общий SDK Reload/config-pull/ACK corpus без REST listener. Отдельный
process сохраняет REST+mTLS и тот же lifecycle contract. Ни один plugin не
обходит Core/peer security boundary прямым вызовом `pluginprotocol`.

### V3: Server и forms-db product development

В v3 возобновить работу в `plugins/server/` и `plugins/forms-db/`; до этого их
текущие v1 behavior/contracts не менять, кроме отдельно одобренного исправления
критического v1 дефекта. Server scope объединяет multi-replica registration,
release compatibility, shared site/CertMagic storage и ACME conformance с
Caddy-L4/public TCP/UDP отдельными gates. forms-db scope объединяет replica и
SQL schema compatibility/rollout для PostgreSQL, MySQL и MariaDB с website/
content evolution. Core остаётся product-agnostic; owner contracts принадлежат
plugins, а общие v2 lifecycle gates покрываются fixtures.

### V3: forms-db website и управление контентом

В v3 отдельно спроектировать развитие forms-db в website/content plugin:
контент принадлежит forms-db storage, административное редактирование доступно
через plugin-owned UI, а endpoint/порт панели отделён от публичной доставки
сайта. Публикацию и HTTP listener необходимо согласовать с Server plugin; Core
хранит только opaque plugin settings и остаётся product-agnostic. Эта задача не
меняет v1 contracts и не предполагает, что текущие права `platform-admin`
автоматически являются пользовательской ролевой моделью сайта.

Порядок: (1) выбрать ownership между одним расширенным forms-db и отдельным
website plugin; (2) согласовать модель контента, schema evolution и DB
migrations; (3) определить, кто обслуживает публичный сайт и кто владеет
отдельным admin listener-ом; (4) спроектировать authN/authZ, provisioning,
roles, audit и network exposure; (5) определить публикацию, immutable release,
`current`/`previous`, rollback и восстановление; (6) обновить owner contracts,
security model и conformance; (7) только затем реализовывать сквозной
Core → Plugin SDK → forms-db/Server сценарий.

Канонические продуктовые вопросы и задачи принадлежат
[forms-db v3 TODO](https://github.com/Liapoldus/forms-db/blob/main/TODO.md) и
[документации forms-db](https://liapoldus.github.io/plugins/forms-db#направление-развития-в-v3-website-и-редактор-содержимого).
Этот roadmap задаёт лишь межсервисную последовательность; он не копирует
forms-db schemas или product API. Ни website/content, ни replica/SQL
compatibility forms-db не входят в v2 agent prompt.

## Правила агентной работы

Агенты могут выполнять независимые вертикальные slices параллельно, но у каждого
среза один файловый/контрактный owner. Разрешено удалять старую архитектуру,
публичные legacy surfaces и код без compatibility shim, если миграционный путь
и владельцы проверены, а replacement contract/tests готовы. Не удалять данные,
чужие dirty/untracked изменения, архивы или репозитории. Изменения проверяются
по AGENTS.md целевого репозитория; вся реализация тестируется по целевому
поведению, не по старой реализации.
