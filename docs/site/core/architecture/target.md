# Целевая архитектура Core v1

Эта страница — каноническая нормативная цель Core v1. Она описывает
архитектуру, к которой должна прийти реализация, и сама по себе не подтверждает
готовность кода. Фактическое состояние и незакрытые проверки ведутся отдельно в
[матрице реализации](implementation) и репозиторных TODO: [Core](https://github.com/Liapoldus/core/blob/main/TODO.md),
[pluginprotocol](https://github.com/Liapoldus/pluginprotocol/blob/main/TODO.md).

После утверждения этой цели старые схемы не поддерживаются параллельно: их
потребители, контракты и тесты переводятся согласованно, а устаревшие API,
fallback и compatibility shims удаляются в том же законченном переходе. Нельзя
объявлять переход завершённым, оставляя одновременно старый и новый lifecycle.

## Модель продукта

Core — единственный экземпляр control plane и единственный источник
desired-конфигурации всех подключённых сервисов. Core сохраняет конфигурацию в
SQLite, строит immutable in-memory generation и сообщает плагинам по REST, что
появилось новое поколение. Плагин сам запрашивает у Core точную версию и
применяет её; Core не отправляет конфигурацию в теле `Reload`. Запросы
пользователя к публичным сайтам не проходят через Management API.

Caddy — реализация отдельного first-party Server plugin process. В v1 plugin
обслуживает только публичный HTTP/HTTPS traffic; L4 и Caddy-L4 исключены из
бинарника и acceptance v1 и переносятся в v2. Core не встраивает Caddy,
не запускает отдельный Caddy binary и не содержит Caddy-specific data plane.
Оператор вручную запускает Server plugin и forms-db binaries; Core подключается
к ним.
В v1 Server plugin имеет ровно одну active replica.

Состав v1 фиксирован: **три запускаемых сервиса** — Core, Server plugin и
forms-db plugin — и **две общие Go-библиотеки** — Plugin SDK и
`pluginprotocol`. CAPTCHA и Identity остаются замороженными вне v1.

| Владелец | Ответственность |
| --- | --- |
| Core | SQLite desired state, Management API/CLI, generic plugin instances/replicas, raw settings generations, endpoints, scoped secret grants, audit и operations. Plugin-to-plugin interaction policies и interaction grants относятся к v2. В v1 Core подключается к вручную запущенным plugin REST endpoints. Core не содержит product-specific branches. |
| Plugin SDK | Отдельный независимый Go-модуль: единый REST/in-process lifecycle contract для `Reload` и exact config pull, health/readiness, schema discovery, метрики, структурированные логи и безопасные ошибки. REST+mTLS используется для отдельных процессов; in-process interface — только для статически связанного плагина в одном процессе и без сетевого mTLS. SDK не управляет process lifecycle, не зависит от `pluginprotocol` и product capabilities. Rollback остаётся Core Management API operation. |
| `pluginprotocol` | Только библиотека plugin↔plugin взаимодействия: generic registration/send/listen/stream, transport abstraction и сетевая защита. Не содержит Core lifecycle/control API, готовых product methods, Manifest, settings, product errors или admin surfaces. |
| Server plugin | HTTP/HTTPS, TLS/ACME, HTTP/2/3, static/proxy, plugin dispatch и опубликованные site artifacts с `current`/`previous`. Caddy — внутренняя технология; Caddy-L4 и публичные TCP/UDP listeners/relay отложены до v2. |
| forms-db plugin | Простые формы, собственные schemas, capabilities и данные; отдельный управляемый оператором сервис. |
| Constructor | Отдельный продукт и клиент Management API; до готовности Core v1 заморожен и в этом этапе не меняется. |

`plugins/captcha` и `plugins/identity` (OIDC/OAuth) полностью заморожены и
исключены из active workspace и v1 до реализации v2. Их исходники, тесты и contracts не
изменяются и не входят в acceptance v1. Это не
отменяет authentication/authorization, mTLS, аудит и redaction Core Management
API.

## Полный scope v2

В v2, а не в v1, входят публичный L4/TCP/UDP relay и Caddy-L4; CAPTCHA и
Identity/OIDC/OAuth; TUF metadata/catalog и установка или обновление plugin
binary через Core; автоматическое управление local processes и deployments в
Docker/Compose, Swarm и Kubernetes. До открытия v2 эти возможности не имеют
активного v1 API, таблиц SQLite, разрешений, бинарных зависимостей или
acceptance gates. Внутренние carriers `pluginprotocol` обслуживают только
межплагинное взаимодействие и не означают публичный L4 relay; их доступный
набор по версиям системы описан в [границе протокола](protocol).

Также только в v2 `pluginprotocol` расширяется локальными IPC carriers:
Unix domain sockets для macOS/Linux и Windows named pipes для Windows. Все
carriers выбираются явно; mTLS обязателен также для локальных соединений, а
filesystem permissions и pipe ACL являются дополнительными ограничениями.
Автоматического выбора и fallback нет. Подробная спецификация и обязательные
platform conformance gates приведены в разделе
[v2: локальные IPC carriers и mTLS](protocol#целевое-расширение-v2-локальные-ipc-carriers).
В v2 также создаётся независимая Python-реализация того же language-neutral
wire contract; она обязана проходить shared vectors и реальные Go↔Python
child-process interop tests для unary и streams на поддерживаемой carrier/OS
матрице. Поддержка других языков не подразумевается автоматически. Полный
процесс описан в разделе
[межъязыковых реализаций](protocol#межъязыковые-реализации-в-v2).

V2 также добавляет единый бинарник/процессный профиль: статически выбранные
доверенные Go plugins могут работать в том же процессе, что и Core, через
in-process adapter Plugin SDK без REST и Core↔plugin mTLS. Отдельно запущенные
или удалённые plugins продолжают использовать REST+mTLS. У обоих адаптеров одна
семантика `Reload` и exact-generation pull; plugin-to-plugin общение остаётся в
`pluginprotocol` с mTLS, без прямого Go-вызова между продуктами. Граница доверия
и conformance описаны в
[v2-модели Plugin SDK](protocol#целевое-расширение-v2-plugin-sdk-без-внутреннего-rest).

## Запуск плагинов в v1

Core остаётся одним экземпляром; active-active и shared SQLite не поддерживаются.
В v1 нет deployment modes и управления workload. Оператор отдельно устанавливает
и вручную запускает Core, Server plugin и forms-db plugin. Оператор отвечает за
их process lifecycle, обновление бинарников, автозапуск после перезагрузки ОС,
перезапуск, лимиты ресурсов и сетевую доступность. Core не получает shell,
process-control, Docker socket или provider credentials и не может запускать,
останавливать, перезапускать, устанавливать, масштабировать либо удалять
плагины. Для каждого plugin instance оператор регистрирует фиксированные REST
endpoint и ожидаемую identity replica. Core выполняет только handshake,
per-replica mTLS, health/readiness, конфигурационный `Reload`, scoped secret
grants и аудит. Централизованные peer policies в v1 отсутствуют. Запуск
плагинов в контейнерах и управление контейнерами не входят в v1.

Локальное process supervision, установка/обновление plugin releases из каталога,
Docker Compose, Docker Swarm, Kubernetes, provider reconciliation, rollout/drain
и mode migration — отдельный v2 scope. Его проектирование не должно добавлять
v1 API, таблицы, permissions или acceptance gates. Общие REST и plugin-to-plugin
контракты остаются одинаковыми и не зависят от будущего способа размещения.

### Ручной запуск и подключение

Оператор устанавливает и запускает Core, затем отдельно устанавливает и
запускает каждый plugin binary средствами ОС или выбранной им службы запуска.
Плагины не получают settings через environment, argv или локальные application
config files. Оператор задаёт Core fixed endpoint и ожидаемую replica identity;
Core проверяет TLS/mTLS, REST manifest/schema и readiness, затем вызывает
`Reload`. Plugin сам запрашивает точное активное поколение у Core, применяет
его и возвращает digest ACK. Перезапуск/обновление plugin выполняет оператор;
после reconnect Core повторно сверяет identity, health и generation, но не
запускает бинарник и не replay-ит пользовательские вызовы.

## REST Reload, поколения конфигурации и восстановление

Для каждого plugin instance Core хранит до трёх полных версий настроек в одной
таблице: `active`, `previous` и внутренний `staging`. `staging` содержит
проверенный candidate, связанный с durable operation, до promotion; он нужен для
crash recovery и никогда не доступен plugin config pull. После validation Core
сохраняет candidate в `staging`, затем отдельной SQLite-транзакцией удаляет
старый `previous`, переносит `active` в `previous` и `staging` в `active`. При
отказе до promotion Core сохраняет прежние `active`/`previous` и удаляет либо
повторно обрабатывает `staging` по состоянию durable operation.
Настройки — raw versioned JSON; Core проверяет синтаксис, generic plugin schema,
CAS и полномочия, но не трактует product fields. Секреты представлены только
внешними references.

1. Management mutation проходит authentication, authorization, schema
   validation, revision CAS, idempotency и audit. Candidate принимается только
   после полной проверки; ошибка не меняет SQLite, in-memory snapshot и не
   вызывает Reload.
2. Core сохраняет исходные candidate bytes в `staging` вместе с durable
   operation. При promotion одна SQLite-транзакция удаляет прежний `previous`,
   переносит прежний `active` в `previous`, а candidate из `staging` — в
   `active`; только после commit Core публикует новое immutable in-memory
   поколение и начинает уведомление replicas.
3. Core вызывает `POST /_liapoldus/v1/reload` у каждой обязательной replica с
   generation, SHA-256 и schema version; JSON body не содержит settings.
   Plugin-side endpoint paths и поля зафиксированы в
   `plugin-sdk/infrastructure/assets/plugin-sdk/v1/http-contract.json`.
4. Plugin SDK делает `GET /internal/v1/plugin-config/{generation}` к Core по
   private mTLS; Core связывает запрос с identity replica и возвращает исходные
   JSON bytes вместе с generation, digest и schema version в headers. Plugin
   полностью проверяет документ и атомарно заменяет свою in-memory
   конфигурацию, затем отвечает точными generation и digest. При ошибке его
   прежняя локальная конфигурация остаётся активной.
5. Частичный успех обрабатывается roll-forward: candidate остаётся `active`;
   подтвердившие replicas обслуживают его, неподтвердившие fenced/degraded и
   получают retry. Core завершает operation после ACK всех обязательных
   replicas; до этого snapshot допускает traffic/peer calls только через
   replicas с нужным active generation.
6. Core Management API `POST /api/plugins/{pluginId}/rollback` начинает новый
   roll-forward на содержимое `previous`: `active` и `previous` атомарно
   меняются местами до Reload. Это Core management
   operation, а не отдельный plugin lifecycle RPC; Core уведомляет replicas
   обычным `Reload(generation)`. Успешные replicas остаются на rollback target,
   отставшие fenced и получают retry; автоматической compensation нет.
7. После рестарта Core проверяет SQLite, journal и digest-ы, строит snapshot и
   продолжает незавершённый rollout вперёд. Он не replay-ит plugin-to-plugin
   Call с неизвестным результатом; оборванный peer Stream закрывается.

Плагин никогда не получает application settings через environment, argv или
application config files: он сам запрашивает их у Core по REST и хранит только
в памяти. Secret Reference не раскрывает значение; отдельный REST secret
redemption проверяет instance, replica, revision, purpose и срок grant. Плагин
удаляет secret bytes при смене revision или shutdown. V1 grants не являются
разрешением на plugin-to-plugin вызов; централизованные call grants отложены
до v2.

## Plugin-to-plugin взаимодействие в v1

`pluginprotocol` даёт plugins общий транспорт, но Core не хранит peer policy,
не формирует peer directory и не авторизует вызовы. Вызывающий plugin владеет
собственной allow/deny policy и передаёт её generic authorizer библиотеки;
default — deny. Вызов идёт напрямую между plugins, Core не стоит на data path и
не пересылает payload. Техническое имя метода и payload schema принадлежат
plugins.

Централизованные `caller → target/method/transport` rules, их generation,
распространение через Core и rollout/drain относятся к v2. Не добавлять для
них v1 endpoints, SQLite tables или Plugin SDK methods.

## `pluginprotocol`: универсальная межплагинная сеть

Единственный владелец wire/transport/security API — репозиторий
[pluginprotocol](https://github.com/Liapoldus/pluginprotocol); namespace
`liapoldus.plugin.v1` сохраняется. Библиотека предоставляет только универсальные
механизмы регистрации произвольных именованных методов/handlers, отправки
сообщения, прослушивания peer endpoint и открытия двунаправленного потока.
Она не содержит готовых продуктовых RPC, имён плагинов, Core API, Manifest,
settings lifecycle, health/readiness endpoints или secret broker.

Публичные application methods не меняются при выборе физического транспорта.
Plugin настраивает профиль соединения отдельно: TCP либо QUIC, лимиты и
deadlines, адреса, аутентификацию и encryption provider. Transport adapters
реализуют один и тот же generic API; конкретный набор поддерживаемых carrier-ов
и их conformance закрепляются в owner-контракте `pluginprotocol`. Core хранит и
выдаёт эти system-level параметры через Plugin SDK, не импортируя protocol Go
пакет.

`pluginprotocol` владеет peer identity, client/server credentials,
сертификатной проверкой и revocation для межплагинных соединений. Для remote
соединений обязательны TLS/mTLS и fail-closed revocation; plaintext downgrade
запрещён. Явное отключение шифрования допускается только для loopback/dev
профиля и никогда не снимает внешний mTLS requirement. Core не является CA;
Management REST использует отдельные trust roots и identities.

## Независимый Plugin SDK и Core↔plugin control plane

Plugin SDK — отдельный Go-модуль, не импортирующий `pluginprotocol`. В v1 он
даёт plugin общий REST server/client, endpoints для Manifest и settings schema,
health/readiness, `Reload`, exact-generation config pull и общие
структуры settings, метрик, структурированных логов и безопасной обработки
исключений. Product-specific settings/capabilities/errors остаются в коде и
контрактах конкретного плагина. Метрики доступны через защищённый scrape
endpoint; runtime logs идут структурированным JSON в stdout/stderr платформы,
а SDK гарантирует redaction чувствительных полей. Необработанное исключение не
возвращает stack/private data клиенту.

В v2 SDK сохраняет REST+mTLS для раздельных процессов и добавляет in-process
adapter для статически скомпонованных plugins в едином Go-процессе. Core
импортирует generic SDK lifecycle facade, а не выбирает транспорт по имени
plugin. Общая последовательность `Reload` → plugin pull-ит точную generation →
ACK не меняется; в in-process режиме config source читает Core in-memory
snapshot через instance-scoped interface. Детали и ограничения границы доверия
зафиксированы в [v2-модели SDK](protocol#целевое-расширение-v2-plugin-sdk-без-внутреннего-rest).

В v1 Core импортирует Plugin SDK REST client, но не `pluginprotocol`. Plugin может
независимо импортировать Plugin SDK, `pluginprotocol`, обе библиотеки или ни одну
из них в зависимости от своих функций. Публикация plugin SDK — отдельный
Go-модуль в workspace `plugin-sdk/`; его canonical Git remote/module path пока
не назначены и не публикуются до отдельного решения владельца.

## Server plugin и durable state

Core хранит Server plugin settings как versioned JSON revisions в своей SQLite.
Server plugin преобразует именно этот JSON в свою Caddy runtime-конфигурацию;
сгенерированный Caddyfile или внутренний Caddy JSON — производный runtime
artifact, не редактируемый source of truth. Ни Caddy Admin API, ни файл
Caddyfile нельзя независимо изменить так, чтобы обойти конфигурацию Core.

Server plugin в v1 вручную запускается оператором как одна replica и хранит на
своём persistent filesystem ACME/CertMagic state и опубликованные site
releases. `current` и `previous` ссылаются на immutable release IDs;
activation/rollback атомарны в границах plugin storage. Оператор отвечает за
persistent filesystem и его backup/restore. PostgreSQL, S3,
shared RWX volume и Caddy HA в v1 не требуются. Масштабирование Caddy выше одной
replica — отдельное будущее изменение: оно потребует общего storage с
распределёнными lock semantics и новым conformance gate; CertMagic координирует
сертификаты между экземплярами только при общем storage
([документация Caddy](https://caddyserver.com/docs/automatic-https)).

Traffic, TLS, public listeners, proxy, static root и WebSocket/SSE settings
меняются как versioned JSON settings Server plugin через generic Core Management
API. Это строгая plugin-owned Liapoldus schema, а не raw Caddy JSON, Caddyfile
или Caddy Admin API payload. Core проверяет опубликованную JSON Schema, CAS и
authorization, но не переводит JSON в Caddy config и не знает product fields.
Caddy-based Server plugin выполняет семантическую валидацию, компилирует документ в private
Caddy runtime config, строит candidate runtime state и применяет её локально
атомарно до ACK. Site artifact операции принадлежат Admin Surface Server plugin;
Core применяет общие authorization, grants, audit и protocol boundary, не
добавляя Caddy-specific group/release API.

Эта schema владеет всеми public HTTP/HTTPS listener-ами; Management bind
остаётся в bootstrap Core. Каждый route имеет ровно один terminal handler:
static release, reverse proxy или plugin capability с объявленным mode.
Middleware chain и общий `continue`/authorization decision отсутствуют в v1:
plugin capability завершает HTTP response. Routes рассматриваются в порядке
массива, первый совпавший route завершается своим terminal handler; предикаты
одного matcher объединяются через AND. Identical matcher отклоняется при
REST `Reload`, overlap разрешается порядком. Продуктовая JSON Schema остаётся
только у Server plugin: raw Caddy JSON/Caddyfile не принимаются. Полная семантика
matchers, handlers, listeners и TLS описана на странице
[Server plugin](../../plugins/server).

HTTP terminal handlers не образуют middleware chain и не retry-ятся через
следующий route. Weighted upstream pool, connect-before-bytes retry, TLS listener
modes и точная path normalization принадлежат Server plugin; plugin-to-plugin
Stream semantics — `pluginprotocol`.

ACME issuance/renewal асинхронны: успешно проверенная traffic revision
активируется, даже если новый сертификат ещё выпускается. Сертификатная
готовность и ошибка ACME отслеживаются отдельно по domain через Server plugin
Admin Surface. Core не обещает собственного `/api/tls` и не реализует issuer;
DNS-01 provider modules выбираются после инвентаризации требований и встраиваются
в Server plugin build. Разрешены ACME challenges, включённые в binary, и custom
certificates только через external secret references; plaintext private key
запрещён в настройках и API.

Site upload — generic Admin Surface action: один multipart request содержит
JSON metadata и один бинарный artifact; Core потоково пересылает artifact по
защищённому REST endpoint Plugin SDK, без Caddy-specific Core endpoint и без
полной буферизации. Длительная публикация возвращает `202` с durable operation ID;
`Idempotency-Key` связывает повтор с той же operation. Лимиты, archive
validation и `current/previous` transitions заданы в [site publishing](../api/sites).

Публичные listener-ы открывает сам Server plugin. Оператор запускает его с
необходимыми OS permissions и обеспечивает публикацию портов; Core не
запускается от root, не передаёт public listener sockets и не управляет
контейнерными ресурсами.

## Management API и безопасность

Management listener полностью отделён от public traffic; Server plugin не
становится reverse proxy для этого API. Core управляет generic plugin
instances/settings, заранее зарегистрированными endpoints, scoped secret
grants, operations, service credentials и audit. Plugin-to-plugin interactions
не входят в API v1. API не
предоставляет Caddy Admin pass-through, Caddy TLS endpoints или Caddy group
endpoints. Caddy-specific действия доступны только как plugin-declared Admin
Surface через общий авторизованный REST action boundary.

Management TLS всегда включён. Web Controller backend соединяется по private
HTTPS с mTLS и отдельным Bearer service credential на binding; credential не
попадает в browser. Desktop Go bridge устанавливает ограниченный SSH
port-forward к loopback Management API, проверяет TLS identity Core и
читает короткоживущий Bearer credential из OS credential store. SSH policy
разрешает только нужный port forward и запрещает shell, SFTP и agent
forwarding. Core проверяет полномочие `platform-admin` на каждом запросе,
пишет audit без token/body/secret и не размещает Management API за Caddy public
listener. Пользовательские роли и environment-scoped permissions принадлежат
Constructor; отдельный Constructor auth redesign отложен и не меняется в этой
итерации.

## SQLite и модель данных

Core — единственный writer и один экземпляр; единственная долгосрочная БД Core
— SQLite на локальном persistent filesystem. В ней находятся bootstrap-derived
metadata, generic plugin instance records, raw desired config generations,
endpoint membership/identity references, `active`/`previous`/internal `staging`
configuration rows, durable operations/idempotency, per-replica generation
acknowledgements, access-key verifiers/metadata и audit. Сырые secret values и
private keys не сохраняются; SQLite содержит только references и non-secret
metadata. Runtime configuration строится в immutable in-memory snapshot;
SQLite не открывается из plugin/Caddy data-plane handler и не читается для
обслуживания пользовательского traffic.

SQLite хранит желаемое/подтверждённое состояние Core, а не runtime state
внешних plugins. Caddy certificate/private-key material и site bytes принадлежат
его persistent directory. Plugin application data остаются в storage самого
plugin; Core хранит только generic endpoint/settings references и состояние
операций. Filesystem не подменяет SQLite для Core config, а Caddy-generated
runtime JSON не подменяет Core desired JSON.

Оператор устанавливает plugin binaries отдельно от Core и хранит их в
операторской системе размещения. Plugin-owned site/certificate data остаются в
storage самого plugin. Plugin configuration хранится только в
`plugin_config_generations(instance_id, generation, slot, raw_json BLOB,
sha256, schema_version, created_at)`. Уникальность `(instance_id, slot)` даёт
не более одной строки каждого из трёх слотов `active`/`previous`/`staging`;
пустой slot представлен отсутствующей строкой. Generation монотонен в пределах
instance. `staging` — durable recovery buffer, но не публикуемый config slot:
plugin может pull-ить только `active` или `previous`.

Management `PUT` принимает raw JSON object напрямую, без общей оболочки. Core
проверяет UTF-8, JSON syntax, размер, duplicate keys и generic plugin-owned
JSON Schema, но сохраняет исходные bytes без decode/remarshal или
каноникализации. Digest вычисляется по точным сохранённым bytes. Plugin config
pull возвращает те же bytes, generation, schema version и digest. Durable
operation хранит только metadata/digest и не дублирует JSON body. Невалидный
документ не меняет `active`/`previous`, не активируется и не вызывает Reload.

При загрузке Core выполняет version/migration check,
SQLite integrity и foreign-key validation, проверяет journal/pointers и digest
всех обязательных Core-owned files, затем восстанавливает in-memory snapshot и
согласует его с plugin ACK. Backup Core включает согласованный SQLite backup и
Core-owned versioned package/config artifacts; backup Caddy release/ACME data
выполняется отдельно на его persistent volume. Никаких PostgreSQL/S3
dependencies для v1.

ER-модель целевого v1 control store показывает `plugin_instance`,
`plugin_config_generations` с slot rows `active`/`previous`/`staging`,
`operation`,
`replica_generation`, `service_key` и `audit_event`. Для plugin config это
утверждённая физическая таблица; отдельная revision table и pointer table не
используются. Её source diagram находится в
`diagrams/core-config-store.mmd`; физические миграции не дублируют desired
JSON в отдельных plugin-specific таблицах.

![ER-модель desired-конфигурации и операций Core](/diagrams/core-config-store.svg)

## Обязательные invariants

- Только Core SQLite является source of truth desired-конфигурации.
- Plugin-specific settings не разбираются и не hardcode-ятся в Core.
- Core всегда один; Server plugin в v1 также один active instance.
- Core уведомляет plugin через REST `Reload`, plugin сам pull-ит immutable
  versioned generation и возвращает exact acknowledgement; config push RPC
  для управления plugin lifecycle не используется.
- `active` и `previous` — единственные публикуемые plugin config slots;
  `staging` хранит недоступный извне candidate для recovery. Partial rollout и
  rollback следуют согласованному roll-forward правилу.
- Все plugin processes вручную запускает и обслуживает оператор. Core никогда
  не выполняет install/start/stop/restart/scale/delete для v1 plugins.
- В v1 Core не централизует peer authorization; plugin-owned authorizer
  применяется fail-closed. Центральный policy plane запланирован в v2.
- Все активные HTTP/HTTPS listener-ы принадлежат Server plugin; Core не является traffic
  proxy и не включает Caddy runtime.
- Public contracts и ошибки не должны обещать отсутствующие lifecycle,
  endpoint или recovery guarantees.
- Никакие ключи, cookie values, Authorization, grants, secret bytes, raw
  private paths или sensitive payloads не раскрываются в log/error/audit.

Подробный порядок миграции и readiness gates находится в
[плане Core v1](v1-migration-roadmap). Protocol source и JSON schemas —
только в [pluginprotocol](https://github.com/Liapoldus/pluginprotocol).
