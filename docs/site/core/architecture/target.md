# Базовые решения Core и переход к v3

Эта страница сохраняет базовые решения Core v2 и переход к v3. Каноническая
нормативная цель production-ready v3 находится в [целевой архитектуре v3](v3);
эта страница не переопределяет её и сама по себе не подтверждает готовность кода.
Фактическое состояние и незакрытые проверки ведутся отдельно в
[матрице реализации](implementation) и репозиторных TODO: [Core](https://github.com/Liapoldus/core/blob/main/TODO.md),
[wool](https://github.com/Liapoldus/wool/blob/main/TODO.md).

После утверждения этой цели старые схемы не поддерживаются параллельно: их
потребители, контракты и тесты переводятся согласованно, а устаревшие API,
fallback и compatibility shims удаляются в том же законченном переходе. Нельзя
объявлять переход завершённым, оставляя одновременно старый и новый lifecycle.

## Модель продукта

Core — единственный экземпляр runtime control plane и источник применённой
desired-конфигурации всех подключённых сервисов. Core сохраняет конфигурацию в
SQLite, строит immutable in-memory generation и сообщает сервисам по REST, что
появилось новое поколение. Сервис сам запрашивает у Core точную версию и
применяет её; Core не отправляет конфигурацию в теле `Reload`. Запросы
пользователя к публичным сайтам не проходят через Management API.

Studio не обращается к Core. Она работает с Project/Git и передаёт immutable
commit в standalone `liapoldus` CLI. CLI materializes canonical config bundle,
выполняет plan/approval и вызывает Core Management API. Core не содержит Git,
project parser, deployment provider или пользовательский CLI.

Caddy — реализация отдельного first-party Server service process. В Core v2 service
обслуживает только публичный HTTP/HTTPS traffic; L4 и Caddy-L4 исключены из
бинарника и текущего acceptance scope. Core не встраивает Caddy,
не запускает отдельный Caddy binary и не содержит Caddy-specific data plane.
Оператор вручную запускает Server service и forms-db binaries; Core подключается
к ним.
В Core v2 Server service имеет ровно одну active replica.

Состав Core v2 фиксирован: **три запускаемых сервиса** — Core, Server service и
forms-db service — и **две общие Go-библиотеки** — Service SDK и
`wool`. CAPTCHA и Identity остаются замороженными вне Core v2.

| Владелец | Ответственность |
| --- | --- |
| Core | SQLite applied desired state, Management API, generic service instances/replicas, raw settings generations, endpoints, scoped secret grants, audit и operations. Config bundle provenance, commit SHA, digest, CAS и idempotency принимаются через API. Git, project parsing и CLI принадлежат standalone CLI. Core не содержит product-specific branches и не управляет процессами/workloads. |
| Service SDK | Отдельный независимый Go-модуль. Предоставляет единый lifecycle contract для REST+mTLS, а также health/readiness, schema discovery, метрики, структурированные логи и безопасные ошибки. SDK не управляет process lifecycle, не зависит от `wool` и product capabilities. Rollback остаётся Core Management API operation. |
| `wool` | Только библиотека service↔service взаимодействия: generic registration/send/listen/stream, transport abstraction и сетевая защита. Не содержит Core lifecycle/control API, готовых product methods, Manifest, settings, product errors или admin surfaces. |
| Server service | HTTP/HTTPS, TLS/ACME, HTTP/2/3, static/proxy, service dispatch и опубликованные site artifacts с `current`/`previous`. Caddy — внутренняя технология; Caddy-L4 и публичные TCP/UDP listeners/relay отложены до v3. |
| forms-db service | Простые формы, собственные schemas, capabilities и данные; отдельный управляемый оператором сервис. |

`services/captcha` и `services/identity` (OIDC/OAuth) полностью заморожены и
исключены из active workspace и Core v2 до явной разморозки. Их исходники, тесты и contracts не
изменяются и не входят в acceptance Core v2. Это не
отменяет authentication/authorization, mTLS, аудит и redaction Core Management
API.

## V2 deployment boundary

При первом старте Core сам один раз создаёт SQLite и initial settings revision
из CLI-provided bootstrap references, затем открывает Management API. Команды
`core init`, `core serve`, `core database` и `core inspect` отсутствуют в Core и
принадлежат standalone CLI target adapters.

`liapoldus apply --project ./project --revision <commit> --target <target>`
читает exact Git revision, строит bundle и отправляет его через API. Core
атомарно записывает bundle и provenance в SQLite, затем запускает обычный
generation/operation/replica rollout. Один и тот же flow используется локально,
для удалённого Core и в GitHub CI; target isolation и remote approval проверяет
CLI/CI до API request.

## Историческая граница v2

Саморегистрация replicas и leases входят в текущий Core runtime. В историческом
v2 deployment workflow rollout и внешние Docker/Swarm/Kubernetes adapters были
отдельными gates, без управления workload из Core. Caller→target link policies
уже являются Core-owned desired state в SQLite и Management API. Studio и
standalone CLI остаются внешними продуктами. Все текущие v3 surfaces — Core
embedding, SDK REST composition и расширенные
product changes — описаны только в [архитектуре v3](v3). Установка и плановые
обновления Core/services принадлежат оператору; Core не управляет workloads.
Core никогда не выполняет autoscaling по нагрузке. До открытия этих этапов нет
дополнительного API, таблиц SQLite, разрешений, бинарных зависимостей или
acceptance gates. Внутренние carriers `wool` обслуживают только
межсервисное взаимодействие и не означают публичный L4 relay; их доступный
набор по версиям системы описан в [границе протокола](protocol).

В v3 планируется продуктовый website/content сценарий для forms-db:
хранение редактируемого контента в принадлежащем service storage, административный
интерфейс на отдельно настраиваемом endpoint/порту и интеграция публикации с
Server service. Это пока направление проектирования, не готовый v3 контракт.
Роли, аутентификация, публичный listener и выпуск/откат site releases должны
быть согласованы до реализации; Core остаётся универсальным и не получает
forms-db-specific модели. Канонический product backlog и открытые решения
ведутся в [forms-db v3 документации](https://liapoldus.github.io/services/forms-db#направление-развития-в-v3-website-и-редактор-содержимого)
и [TODO forms-db](https://github.com/Liapoldus/forms-db/blob/main/TODO.md).

В v2 `wool` расширяется локальными IPC carriers:
Unix domain sockets для macOS/Linux и Windows named pipes для Windows. Все
carriers выбираются явно; для production и remote connections требуется mTLS.
`wool` допускает отдельный, явно выбранный plaintext-профиль для TCP
loopback в development. SDK WIP также содержит отдельный v2 loopback plaintext
profile для Core↔service REST: только отдельный listener с literal loopback TCP
address и generic `GET /healthz`; по умолчанию он выключен.
Config pull, secret grants и все остальные SDK endpoints остаются mTLS-only.
Профиль ещё не входит в опубликованную SDK/Core dependency revision и не
считается доступным в поддерживаемом Core runtime до совместимого pin и
сквозного conformance.
Unix socket и Windows named pipe требуют mTLS даже при локальном размещении;
filesystem permissions и pipe ACL являются дополнительными ограничениями.
Автоматического выбора, fallback или downgrade нет. Подробная спецификация и обязательные
platform conformance gates приведены в разделе
[v2: локальные IPC carriers и mTLS](protocol#целевое-расширение-v2-локальные-ipc-carriers).

## V3: Core host API

Core host API использует штатный runtime, SQLite и Management API.
Все runtime Services подключаются через Service SDK REST+mTLS.
Control plane: Studio → CLI → Core → Service SDK REST.
Data plane: независимые Services → Wool. StudioPlugins расширяют редактор.

## Запуск сервисов в Core v2

Core остаётся одним экземпляром; active-active и shared SQLite не поддерживаются.
В Core v2 нет deployment modes и управления workload. Оператор отдельно устанавливает
и вручную запускает Core, Server service и forms-db service. Оператор отвечает за
их process lifecycle, обновление бинарников, автозапуск после перезагрузки ОС,
перезапуск, лимиты ресурсов и сетевую доступность. Core не получает shell,
process-control, Docker socket или provider credentials и не может запускать,
останавливать, перезапускать, устанавливать, масштабировать либо удалять
сервисы. Каждая replica регистрирует REST endpoint и подтверждённую mTLS
identity через Service SDK. Core выполняет только handshake,
per-replica mTLS, health/readiness, конфигурационный `Reload`, scoped secret
grants и аудит. Централизованные peer policies принадлежат следующему этапу.
Запуск сервисов в контейнерах и управление контейнерами не входят в Core v2.

Candidate rollout с traffic weights и deployment профили Docker/Swarm/Kubernetes
внешне управляемых workloads — следующий этап; установку и обновление выполняет оператор.
Локальное process supervision Core не планируется. Проектирование не добавляет
дополнительные API, таблицы, permissions или acceptance gates. Общие REST и service-to-service
контракты остаются одинаковыми и не зависят от будущего способа размещения.

### Ручной запуск и подключение

Оператор устанавливает Core и каждый service отдельно. При первичном запуске он
сначала запускает service binaries средствами ОС или выбранной службы, проверяет
их control-plane health и mTLS identity, затем запускает Core. Сервисы не
получают settings через environment, argv или локальные application config
files. Core проверяет TLS/mTLS, REST manifest/schema и readiness, затем вызывает
`Reload`. Service сам запрашивает точное активное поколение у Core, применяет
его и возвращает digest ACK. Перезапуск/обновление service выполняет оператор;
Core проверяет readiness при запуске и затем периодически только для наблюдения
состояния и фиксации drift; интервал задаётся versioned contract asset. Монитор
не вызывает `Reload` и не повторяет неуспешные операции. Если service перезапущен
отдельно, пока Core продолжает работать, он останется degraded до ручного
перезапуска Core: startup reconciliation сверит identity, health и active
generation и вызовет `Reload` при расхождении. Core не запускает бинарник и не
replay-ит пользовательские вызовы.

## REST Reload, поколения конфигурации и восстановление

Для каждого service instance Core хранит до трёх полных версий настроек в одной
таблице: `active`, `previous` и внутренний `staging`. `staging` содержит
проверенный candidate, связанный с durable operation, до promotion; он нужен для
crash recovery и никогда не доступен service config pull. После validation Core
сохраняет candidate в `staging`, затем отдельной SQLite-транзакцией удаляет
старый `previous`, переносит `active` в `previous` и `staging` в `active`. При
отказе до promotion Core сохраняет прежние `active`/`previous` и удаляет либо
повторно обрабатывает `staging` по состоянию durable operation.
Настройки — raw versioned JSON; Core проверяет синтаксис, generic service schema,
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
   Service-side endpoint paths и поля зафиксированы в
   `service-sdk/infrastructure/assets/service-sdk/v2/http-contract.json`.
4. Service SDK делает `GET /internal/v1/service-config/{generation}` к Core по
   private mTLS; Core связывает запрос с identity replica и возвращает исходные
   JSON bytes вместе с generation, digest и schema version в headers. Service
   полностью проверяет документ и атомарно заменяет свою in-memory
   конфигурацию, затем отвечает точными generation и digest. При ошибке его
   прежняя локальная конфигурация остаётся активной.
5. Частичный успех обрабатывается roll-forward: candidate остаётся `active`;
   подтвердившие replicas обслуживают его, неподтвердившие fenced/degraded и
   получают retry. Core завершает operation после ACK всех обязательных
   replicas; до этого snapshot допускает traffic/peer calls только через
   replicas с нужным active generation.
6. Core Management API `POST /api/services/{serviceId}/rollback` начинает новый
   roll-forward на содержимое `previous`: `active` и `previous` атомарно
   меняются местами до Reload. Это Core management
   operation, а не отдельный service lifecycle RPC; Core уведомляет replicas
   обычным `Reload(generation)`. Успешные replicas остаются на rollback target,
   отставшие fenced и получают retry; автоматической compensation нет.
7. После рестарта Core проверяет SQLite, journal и digest-ы, строит snapshot и
   продолжает незавершённый rollout вперёд. Он не replay-ит service-to-service
   Call с неизвестным результатом; оборванный peer Stream закрывается.

Сервис никогда не получает application settings через environment, argv или
application config files: он сам запрашивает их у Core по REST и хранит только
в памяти. Secret Reference не раскрывает значение; отдельный REST secret
redemption проверяет instance, replica, revision, purpose и срок grant. Сервис
удаляет secret bytes при смене revision или shutdown. V1 grants не являются
разрешением на service-to-service вызов; централизованные call grants отложены
до следующего этапа.

## Service-to-service взаимодействие

`wool` даёт services общий транспорт, а Core владеет generic
`caller → target/method/transport` policy и публикует caller-scoped peer
directory через Service SDK. Вызывающий service передаёт разрешённый контекст
generic authorizer библиотеки; default — deny. Вызов идёт напрямую между
services, Core не стоит на data path и не пересылает payload. Техническое имя
метода и payload schema принадлежат services.

## `wool`: универсальная межсервисная сеть

Единственный владелец wire/transport/security API — репозиторий
[wool](https://github.com/Liapoldus/wool); namespace
`liapoldus.peer.v1` используется для generic peer wire contract. Библиотека
предоставляет только универсальные
механизмы регистрации произвольных именованных методов/handlers, отправки
сообщения, прослушивания peer endpoint и открытия двунаправленного потока.
Она не содержит готовых продуктовых RPC, имён сервисов, Core API, Manifest,
settings lifecycle, health/readiness endpoints или secret broker.

Публичные application methods не меняются при выборе физического транспорта.
Service настраивает профиль соединения отдельно: TCP либо QUIC, лимиты и
deadlines, адреса, аутентификацию и encryption provider. Transport adapters
реализуют один и тот же generic API; конкретный набор поддерживаемых carrier-ов
и их conformance закрепляются в owner-контракте `wool`. Core хранит и
выдаёт эти system-level параметры через Service SDK, не импортируя protocol Go
пакет.

`wool` владеет peer identity, client/server credentials,
сертификатной проверкой и revocation для межсервисных соединений. Для remote
соединений и всех production-профилей обязательны TLS/mTLS и fail-closed
revocation; plaintext downgrade запрещён. Peer-протокол допускает явно
выбранный plaintext-профиль для TCP loopback в development; он не шифрует и не
аутентифицирует соединение. SDK WIP содержит отдельный loopback-only plaintext
development profile для Core↔service REST, но опубликованная dependency revision
Core его пока не предоставляет. В профиле разрешён только generic health
endpoint; config pull и secret grants всегда защищены mTLS. QUIC всегда
зашифрован и требует взаимной аутентификации, а локальные IPC в v2 не имеют
plaintext-профиля.

TLS реализации принадлежат библиотекам: Service SDK выполняет TLS/mTLS для
Core↔service REST, `wool` — для service↔service carriers. Service передаёт
настройки профиля и credentials через API соответствующей библиотеки, но не
создаёт свой TLS stack и не повторяет проверку сертификатов в product-коде.
Trust roots SDK REST и peer protocol независимы. Core не является CA;
Management REST имеет собственные trust roots и identities. In-process вызовы
Service SDK в v3 не образуют сетевую границу и не используют mTLS между Core и
service; peer-соединения и там остаются на `wool`.

## Независимый Service SDK и Core↔service control plane

Service SDK — отдельный Go-модуль, не импортирующий `wool`. В Core v2 он
даёт service общий REST server/client, endpoints для Manifest и settings schema,
health/readiness, `Reload`, exact-generation config pull и общие
структуры settings, метрик, структурированных логов и безопасной обработки
исключений. Product-specific settings/capabilities/errors остаются в коде и
контрактах конкретного сервиса. Метрики доступны через защищённый scrape
endpoint; runtime logs идут структурированным JSON в stdout/stderr платформы,
а SDK гарантирует redaction чувствительных полей. Необработанное исключение не
возвращает stack/private data клиенту.

Core использует Service SDK REST+mTLS для Reload, exact-generation pull и ACK.
Generation bytes/digests остаются неизменяемыми.

В Core v2 Core импортирует Service SDK REST client, но не `wool`. Service может
независимо импортировать Service SDK, `wool`, обе библиотеки или ни одну
из них в зависимости от своих функций. Утверждённый canonical module path SDK —
`github.com/Liapoldus/service-sdk/v2`; текущий временный local path и imports должны
быть мигрированы согласованно до production release.

## Server service и durable state

Core хранит Server service settings как versioned JSON revisions в своей SQLite.
Server service преобразует именно этот JSON в свою Caddy runtime-конфигурацию;
сгенерированный Caddyfile или внутренний Caddy JSON — производный runtime
artifact, не редактируемый source of truth. Ни Caddy Admin API, ни файл
Caddyfile нельзя независимо изменить так, чтобы обойти конфигурацию Core.

Server service хранит на своём persistent filesystem ACME/CertMagic state и опубликованные site
releases. `current` и `previous` ссылаются на immutable release IDs;
activation/rollback атомарны в границах service storage. Оператор отвечает за
persistent filesystem и его backup/restore. PostgreSQL, S3,
shared RWX volume и Caddy HA в Core v2 не требуются. Масштабирование Caddy выше одной
replica — отдельное будущее изменение: оно потребует общего storage с
распределёнными lock semantics и новым conformance gate; CertMagic координирует
сертификаты между экземплярами только при общем storage
([документация Caddy](https://caddyserver.com/docs/automatic-https)).

Traffic, TLS, public listeners, proxy, static root и WebSocket/SSE settings
меняются как versioned JSON settings Server service через generic Core Management
API. Это строгая service-owned Liapoldus schema, а не raw Caddy JSON, Caddyfile
или Caddy Admin API payload. Core проверяет опубликованную JSON Schema, CAS и
authorization, но не переводит JSON в Caddy config и не знает product fields.
Caddy-based Server service выполняет семантическую валидацию, компилирует документ в private
Caddy runtime config, строит candidate runtime state и применяет её локально
атомарно до ACK. Site artifact операции принадлежат Admin Surface Server service;
Core применяет общие authorization, grants, audit и protocol boundary, не
добавляя Caddy-specific group/release API.

Эта schema владеет всеми public HTTP/HTTPS listener-ами; Management bind
остаётся в bootstrap Core. Каждый route имеет ровно один terminal handler:
static release, reverse proxy или service capability с объявленным mode.
Middleware chain и общий `continue`/authorization decision не входят в Core v2:
service capability завершает HTTP response. Routes рассматриваются в порядке
массива, первый совпавший route завершается своим terminal handler; предикаты
одного matcher объединяются через AND. Identical matcher отклоняется при
REST `Reload`, overlap разрешается порядком. Продуктовая JSON Schema остаётся
только у Server service: raw Caddy JSON/Caddyfile не принимаются. Полная семантика
matchers, handlers, listeners и TLS описана на странице
[Server service](../../services/server).

HTTP terminal handlers не образуют middleware chain и не retry-ятся через
следующий route. Weighted upstream pool, connect-before-bytes retry, TLS listener
modes и точная path normalization принадлежат Server service; service-to-service
Stream semantics — `wool`.

ACME issuance/renewal асинхронны: успешно проверенная traffic revision
активируется, даже если новый сертификат ещё выпускается. Сертификатная
готовность и ошибка ACME отслеживаются отдельно по domain через Server service
Admin Surface. Core не обещает собственного `/api/tls` и не реализует issuer;
DNS-01 provider modules выбираются после инвентаризации требований и встраиваются
в Server service build. Разрешены ACME challenges, включённые в binary, и custom
certificates только через external secret references; plaintext private key
запрещён в настройках и API.

Site upload — generic Admin Surface action: один multipart request содержит
JSON metadata и один бинарный artifact; Core потоково пересылает artifact по
защищённому REST endpoint Service SDK, без Caddy-specific Core endpoint и без
полной буферизации. Длительная публикация возвращает `202` с durable operation ID;
`Idempotency-Key` связывает повтор с той же operation. Лимиты, archive
validation и `current/previous` transitions заданы в [site publishing](../api/sites).

Публичные listener-ы открывает сам Server service. Оператор запускает его с
необходимыми OS permissions и обеспечивает публикацию портов; Core не
запускается от root, не передаёт public listener sockets и не управляет
контейнерными ресурсами.

## Management API и безопасность

Management listener полностью отделён от public traffic; Server service не
становится reverse proxy для этого API. Core управляет generic service
instances/settings, заранее зарегистрированными endpoints, scoped secret
grants, operations, service credentials и audit. Service-to-service interactions
не входят в Core Management API. API не
предоставляет Caddy Admin pass-through, Caddy TLS endpoints или Caddy group
endpoints. Caddy-specific действия доступны только как service-declared Admin
Surface через общий авторизованный REST action boundary.

Management TLS всегда включён. `liapoldus` CLI соединяется по private HTTPS с
mTLS и отдельным Bearer service credential на target; credential не попадает в
Studio или browser. Для remote targets CLI может использовать ограниченный SSH
port-forward или managed agent, если это объявлено target capability; SSH policy
запрещает shell, SFTP и agent forwarding. Core проверяет полномочие
`platform-admin` на каждом запросе,
пишет audit без token/body/secret и не размещает Management API за Caddy public
listener. Пользовательские роли и environment-scoped permissions не входят в
Core v2.

## SQLite и модель данных

Core — единственный writer и один экземпляр; единственная долгосрочная БД Core
— SQLite на локальном persistent filesystem. В ней находятся bootstrap-derived
metadata, generic service instance records, raw desired config generations,
endpoint membership/identity references, `active`/`previous`/internal `staging`
configuration rows, durable operations/idempotency, per-replica generation
acknowledgements, access-key verifiers/metadata и audit. Сырые secret values и
private keys не сохраняются; SQLite содержит только references и non-secret
metadata. Runtime configuration строится в immutable in-memory snapshot;
SQLite не открывается из service/Caddy data-plane handler и не читается для
обслуживания пользовательского traffic.

SQLite хранит желаемое/подтверждённое состояние Core, а не runtime state
внешних services. Caddy certificate/private-key material и site bytes принадлежат
его persistent directory. Service application data остаются в storage самого
service; Core хранит только generic endpoint/settings references и состояние
операций. Filesystem не подменяет SQLite для Core config, а Caddy-generated
runtime JSON не подменяет Core desired JSON.

В v2 каждая применённая конфигурация также хранит безопасную provenance:
project/repository identity, immutable commit SHA, canonical bundle digest,
bundle schema version, target/environment metadata, actor и operation link. Эти
поля приходят от standalone CLI через Management API и записываются вместе с
desired configuration в одной транзакции. Git остаётся source of source files;
SQLite остаётся source of applied runtime state.

Оператор устанавливает service binaries отдельно от Core и хранит их в
операторской системе размещения. Service-owned site/certificate data остаются в
storage самого service. Service configuration хранится только в
`service_config_generations(instance_id, generation, slot, raw_json BLOB,
sha256, schema_version, created_at)`. Уникальность `(instance_id, slot)` даёт
не более одной строки каждого из трёх слотов `active`/`previous`/`staging`;
пустой slot представлен отсутствующей строкой. Generation монотонен в пределах
instance. `staging` — durable recovery buffer, но не публикуемый config slot:
service может pull-ить только `active` или `previous`.

Management `PUT` принимает raw JSON object напрямую, без общей оболочки. Core
проверяет UTF-8, JSON syntax, размер, duplicate keys и generic service-owned
JSON Schema, но сохраняет исходные bytes без decode/remarshal или
каноникализации. Digest вычисляется по точным сохранённым bytes. Service config
pull возвращает те же bytes, generation, schema version и digest. Durable
operation хранит только metadata/digest и не дублирует JSON body. Невалидный
документ не меняет `active`/`previous`, не активируется и не вызывает Reload.

При загрузке Core выполняет version/migration check,
SQLite integrity и foreign-key validation, проверяет journal/pointers и digest
всех обязательных Core-owned files, затем восстанавливает in-memory snapshot и
согласует его с service ACK. Backup Core включает согласованный SQLite backup и
Core-owned versioned package/config artifacts; backup Caddy release/ACME data
выполняется отдельно на его persistent volume. У Core v2 нет PostgreSQL/S3
dependencies; product services могут иметь собственный storage scope.

ER-модель целевого Core v2 control store показывает `service_instance`,
`service_config_generations` с slot rows `active`/`previous`/`staging`,
`operation`,
`replica_generation`, `service_key` и `audit_event`. Для service config это
утверждённая физическая таблица; отдельная revision table и pointer table не
используются. Её source diagram находится в
`diagrams/core-config-store.mmd`; физические миграции не дублируют desired
JSON в отдельных service-specific таблицах.

![ER-модель desired-конфигурации и операций Core](/diagrams/core-config-store.svg)

## Обязательные invariants

- Только Core SQLite является source of truth desired-конфигурации.
- Service-specific settings не разбираются и не hardcode-ятся в Core.
- Core всегда один; Server service в Core v2 также один active instance.
- Core уведомляет service через REST `Reload`, service сам pull-ит immutable
  versioned generation и возвращает exact acknowledgement; config push RPC
  для управления service lifecycle не используется.
- `active` и `previous` — единственные публикуемые service config slots;
  `staging` хранит недоступный извне candidate для recovery. Partial rollout и
  rollback следуют согласованному roll-forward правилу.
- Все service processes вручную запускает и обслуживает оператор. Core никогда
  не выполняет install/start/stop/restart/scale/delete для services.
- В Core v2 Core не централизует peer authorization; service-owned authorizer
  применяется fail-closed. Центральный policy plane запланирован на следующий этап.
- Все активные HTTP/HTTPS listener-ы принадлежат Server service; Core не является traffic
  proxy и не включает Caddy runtime.
- Public contracts и ошибки не должны обещать отсутствующие lifecycle,
  endpoint или recovery guarantees.
- Никакие ключи, cookie values, Authorization, grants, secret bytes, raw
  private paths или sensitive payloads не раскрываются в log/error/audit.

Подробный порядок миграции и readiness gates находится в
[плане Core v2](roadmap). Protocol source и JSON schemas —
только в [wool](https://github.com/Liapoldus/wool).
