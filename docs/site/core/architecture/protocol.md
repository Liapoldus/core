# Плагинные библиотеки и граница взаимодействия

В системе есть две независимые Go-библиотеки с разными назначениями. Общий
Core↔plugin lifecycle принадлежит Plugin SDK: отдельные процессы используют
REST; in-process adapter для монолитной композиции относится к следующему
продуктовому этапу.
Универсальная
межплагинная сеть принадлежит `pluginprotocol`. Ни одна из библиотек не владеет
контрактами конкретных продуктов: схемы и методы CAPTCHA, Caddy, forms-db и
identity определяются только соответствующими плагинами.

Каноническая ownership-модель приведена в [целевой архитектуре](target),
состояние Core и поколения — в [control-plane](control-plane), а фактические
блокеры и команды проверки — в [матрице приёмки](../configuration/acceptance).
Эта страница фиксирует границы, не дублируя protobuf, JSON schemas или REST
OpenAPI конкретных владельцев.

## Две независимые библиотеки

| Библиотека | Ответственность | Что не входит |
| --- | --- | --- |
| Plugin SDK, отдельный Go-модуль | Общий REST server/client, bootstrap, health/readiness, settings schema discovery, exact config pull, `Reload`, метрики, структурированные логи и безопасные ошибки. В следующем этапе тот же lifecycle contract может получить явно выбранный in-process adapter для монолитной сборки. | Межплагинный transport, Core Management API operations и продуктовые capabilities. |
| `pluginprotocol` | Generic plugin-to-plugin communication: регистрация пользовательских методов/handlers, connect/listen, unary calls и streams, физические transport/security providers. | Core lifecycle REST, config distribution, Manifest/settings/admin surfaces, готовые product RPC или имена plugin methods. |

Plugin SDK и `pluginprotocol` не импортируют друг друга. Плагин использует одну
библиотеку, обе или ни одну — в зависимости от того, нужен ли ему общий REST
lifecycle и/или прямое взаимодействие с другими плагинами. Core использует
Plugin SDK через REST client. Core не импортирует `pluginprotocol`.

TLS/mTLS реализуют библиотеки, а не конкретные плагины: Plugin SDK владеет
Core↔plugin REST security, `pluginprotocol` — security peer carriers. Plugin
передаёт нужной библиотеке bootstrap security configuration и credential source
через её API; product handlers не создают собственный TLS stack, не выполняют
handshake и не повторяют проверку сертификатов. Trust roots этих двух каналов
раздельны. Security profile выбирается в конфигурации соответствующей
библиотеки, отдельно от product settings; plugin только передаёт профиль и
credential source её API. Production и удалённые Core↔plugin REST connections
используют per-replica mTLS по действующему v1 contract. SDK WIP реализует
отдельный versioned loopback-only plaintext development profile: он opt-in,
выключен по умолчанию и на отдельном literal-loopback TCP listener обслуживает
только generic `GET /_liapoldus/v1/health`. `/ready` остаётся mTLS-only из-за
identity/generation полей ответа; redacted readiness view потребует отдельного
versioned contract. Exact config pull и secret-grant обмен всегда используют
mTLS. Профиль пока отсутствует в опубликованной SDK revision, закреплённой
Core, поэтому не доступен в текущем Core release. Peer protocol
уже допускает явный TCP-loopback plaintext в development без encryption и peer
identity. QUIC всегда зашифрован и аутентифицирован; v2 Unix socket/named pipe
пока требуют mTLS. Ни один профиль не переключается автоматически после ошибки
TLS.

Локальное расположение модуля — соседний каталог `plugin-sdk/` workspace.
Утверждённый canonical Go module path — `github.com/Liapoldus/plugin-sdk`;
согласованная миграция текущих local imports остаётся частью Core v2.

## Plugin SDK: REST lifecycle

Каждый plugin предоставляет защищённый control endpoint по общему REST
контракту SDK. Core адресует каждую зарегистрированную replica отдельно;
Core↔plugin REST использует mTLS и уникальную identity каждой replica. В Core v2
оператор выдаёт credentials через внешний CA/PEM source; Core не выпускает
identity и не внедряет её через process/container bootstrap. Trust roots control
plane отделены от plugin-to-plugin trust roots. Plaintext или bearer-only
downgrade не допускается.
Endpoint доступен только Core management identity и не является пользовательским
traffic API.

SDK contract source — `plugin-sdk/infrastructure/assets/plugin-sdk/v1/http-contract.json`.
В нём заданы plugin-side endpoints:

- bootstrap identity/version, Manifest и settings schema;
- health и readiness;
- `POST /_liapoldus/v1/reload` с generation, digest и schema version;
- exact-generation pull: plugin делает `GET /internal/v1/plugin-config/{generation}` к Core;
- readiness/ACK и bounded REST errors; запуск, остановка и drain процесса
  выполняются оператором вне Core;
- общих метрик, структурированных логов и безопасных error/problem responses.

Core сначала сохраняет исходный JSON в SQLite и продвигает active generation,
затем вызывает `Reload` без передачи документа. Config pull доступен только
по per-replica mTLS и возвращает точные исходные bytes; plugin валидирует и
применяет их в памяти, после чего ACK-ает generation/digest. Слоты
`active`/`previous`, roll-forward при частичном результате, rollback,
idempotency, retries и fencing нормативно определены в
[control-plane](control-plane). REST endpoint shapes и wire JSON принадлежат
Plugin SDK contract.

SDK даёт общие primitives, но не выбирает product settings и не создаёт
необязательные настройки за plugin. Plugin сам владеет versioned JSON Schema,
значениями полей, validation и runtime application. Общие логи не должны
содержать settings, cookies, credentials, tokens, private keys, grant handles
или secret bytes. Непредвиденная ошибка возвращается как безопасный типовой
problem, а диагностические детали остаются в redacted server-side logs.

## Целевое расширение v3: in-process adapter и монолитная композиция

В v3 Plugin SDK должен поддерживать один transport-independent lifecycle
contract с двумя явно выбираемыми адаптерами:

| Adapter | Применение | Транспортная граница |
| --- | --- | --- |
| REST | Plugin работает отдельным процессом, удалённым workload или управляется внешним deployment mode. | Core и plugin — отдельные security principals; используется REST по mTLS, уникальные replica identities и существующая TLS-проверка. |
| In-process | Plugin статически включён в единый бинарник и работает в том же Go-процессе, что и Core. | Прямые вызовы SDK interfaces; HTTP listener, socket, TLS и mTLS между Core и plugin не создаются. |

Это две реализации одного lifecycle API, а не два разных набора правил. Adapter
выбирается явно для каждого plugin instance при сборке/композиции запуска; ошибки
выбранного adapter-а не вызывают автоматическое переключение. V1 продолжает
использовать REST. Единый executable, который запускает plugin как дочерний
процесс, не является in-process режимом: между процессами остаётся REST, пока
отдельным решением не введён иной IPC adapter.

In-process adapter сохраняет pull-семантику поколений:

1. Core завершает durable commit и публикует immutable in-memory generation.
2. Core вызывает lifecycle interface plugin-а с `Reload` и только метаданными
   поколения (generation, digest, schema version); raw config не передаётся
   аргументом `Reload`.
3. В обработчике `Reload` plugin вызывает scoped `ConfigSource` SDK для точного
   поколения. In-process реализация `ConfigSource` читает Core snapshot, а не
   SQLite напрямую. Она закреплена за одним plugin instance и не позволяет
   запрашивать конфигурацию другой instance.
4. Plugin проверяет исходные bytes, применяет конфигурацию в своей памяти и
   возвращает ACK с теми же generation и digest. Ошибки, CAS, idempotency,
   roll-forward, rollback и operation status имеют ту же семантику, что и REST.

REST и in-process adapters должны использовать общие модели/интерфейсы SDK для
generation metadata, raw config, ACK, health/readiness и классификации ошибок.
Транспортные DTO REST не становятся SDK domain types; signatures фиксируются в
Plugin SDK contract. Core вызывает только generic SDK interface и не содержит
ветвлений по именам/типам конкретных plugins. Composition root связывает
статически включённые plugin factories с Core; список включённых модулей
определяется сборкой, а не динамическим загрузчиком Go `plugin`.

In-process не является криптографической или process-isolation boundary.
Instance identity задаётся immutable host binding при композиции; Core всё равно
проверяет instance scope, generation, grants и permissions до выдачи данных.
Trust roots и mTLS не применяются только к прямым Core↔plugin вызовам внутри
того же процесса. Plugin получает доверие как скомпилированный код: panic можно
перехватить на adapter boundary и преобразовать в безопасную ошибку, но это не
защищает Core от исчерпания памяти, бесконечной работы, нарушения памяти или
аварийного завершения процесса. Такой режим допустим только для доверенных
first-party/оператором включённых модулей.

Изменение не затрагивает `pluginprotocol`. Межплагинные calls и streams остаются
на generic peer API и явно выбранном защищённом carrier-е с mTLS, даже если
участники собраны в один executable. Прямые Go-вызовы или Go channels между
разными plugins запрещены как скрытый обход peer identity, authorization,
method registry и transport conformance. Возможный in-process carrier для
plugin↔plugin потребует отдельного изменения `pluginprotocol`, security model и
pairwise conformance; он не входит в это решение.

V3 implementation gate: один и тот же SDK conformance corpus проходит через
REST-child-process и in-process adapters; проверяются exact-generation pull,
raw bytes/digest, ACK, cancellation, failure mapping, panic containment,
authorization scope и запрет доступа к чужому instance. Дополнительно Docker/
deployment профили подтверждают, что удалённые плагины продолжают использовать
REST+mTLS, а локально скомпонованные — in-process без listener-ов и без
ослабления plugin-to-plugin mTLS.

## `pluginprotocol`: generic plugin-to-plugin network

`pluginprotocol` — самостоятельная Go-библиотека для прямого обмена между
плагинами. В ней нет predeclared бизнес-методов: plugin регистрирует собственные
имена методов и обработчики, а peer вызывает их по общему transport API.
Библиотека не должна знать, какие продукты существуют, каковы их capability
names, какие у них settings, кто является «Caddy» или «identity», и как
выглядят их payloads.

Прикладной метод и его request/response schema принадлежат плагину, который его
объявил. `pluginprotocol` предоставляет только общую оболочку вызова и
транспортные примитивы. Одни и те же application-level method names, payloads,
ошибки и stream semantics должны сохраняться при смене физического carrier-а.
Конкретные версии транспорта и security profiles закрепляются в owner-контракте
самого `pluginprotocol` после conformance-проверок; эта страница не фиксирует
непринятый wire format или API signatures.

До открытия соединения библиотека проверяет общий peer policy/configuration,
identity и разрешённый carrier. Для удалённых workloads и production применяется
аутентифицированное шифрованное соединение; отсутствие encryption или
неизвестный транспортный профиль не может стать silent downgrade. Явный
loopback TCP plaintext development profile допускается также в v2: он не
аутентифицирует peer и не может обслуживать удалённый адрес. QUIC остаётся
зашифрованным и аутентифицированным, а локальные IPC используют mTLS. SDK
loopback plaintext development profile для Core REST — отдельное целевое
расширение, не входящее в текущий production contract. Межплагинный trust не разделяет
trust roots с Core REST.

В Core v2 caller→target/method/transport policies хранятся в Core SQLite,
авторизуются через Management API и распространяются через SDK-defined
directory. Plugin не владеет центральной policy и получает только разрешённую
выборку для своей replica. `pluginprotocol` исполняет выбранный carrier.
`pluginprotocol` только исполняет выбранный разрешённый carrier и не получает
Core policy storage/API. Core не является CA и не стоит между peers как data
proxy. `pluginprotocol` не выдаёт Core settings, не делает config pull и не
предоставляет `Reload`.

Secret grants относятся только к раскрытию opaque secret references из
plugin settings и выдаются через Plugin SDK REST по его owner contract. Они не
авторизуют межплагинные вызовы. Call-scoped plugin-to-plugin grants и их
централизованная выдача относятся к следующему этапу; `pluginprotocol` не выпускает,
валидирует или погашает grants.

## Физический transport и безопасность

Физический канал настраивается отдельно от прикладных методов. Plugin выбирает
поддержанный library carrier и security provider в runtime bootstrap, сохраняя
зарегистрированные handlers и payload contracts. Добавление carrier-а или
изменение listen/dial-параметров не должно требовать переписывать product
handlers.

Окончательный carrier matrix, поля профиля, timeout/backpressure defaults,
credential providers, rotation и revocation должны быть нормативно описаны в
`pluginprotocol` и покрыты conformance до production реализации. Уже принятое
требование: удалённая связь защищена и аутентифицирует peer; Core REST и
plugin-to-plugin connections используют разные identities и trust roots.

Конкретный transport SDK организуется четырьмя слоями `domain`, `application`,
`infrastructure`, `presentation` согласно `pluginprotocol/AGENTS.md`. Domain
содержит только модели и interfaces; carrier, криптография, sockets и TLS
реализуются infrastructure adapters; presentation экспонирует публичный
library facade. Product contracts в этот модуль не переносятся.

### Следующий этап: локальные IPC carriers

Поддержка локального IPC относится к следующему этапу. Она не является частью
Core v2 и не должна добавляться как скрытый fallback или предварительный кодовый
путь. До отдельного утверждения реализация и контракты Core v2 не меняются.

В v2 `pluginprotocol` предоставляет четыре явно выбираемых carrier-а:

| Carrier | Область применения | Защита канала |
| --- | --- | --- |
| TCP | Сетевое взаимодействие между hosts/processes | TLS/mTLS поверх stream connection. |
| QUIC | Сетевое взаимодействие с multiplexing/datagram semantics | TLS/mTLS, встроенные в QUIC; второй TLS-слой не добавляется. |
| Unix domain socket | Локальные процессы на поддерживаемых Unix-платформах, включая macOS и Linux | Тот же TLS/mTLS поверх stream connection; filesystem ownership/mode — дополнительное ограничение доступа. |
| Windows named pipe | Локальные процессы Windows | Тот же TLS/mTLS поверх pipe stream; Windows pipe ACL — дополнительное ограничение доступа. |

«Socket» не является отдельным неоднозначным видом транспорта: Unix domain
socket и Windows named pipe имеют отдельные carrier identifiers, endpoint
форматы и platform-specific infrastructure adapters. Carrier выбирается явно
в runtime-конфигурации. Автоматический выбор, переключение между carriers и
fallback после ошибки соединения запрещены. Application method names,
payloads, зарегистрированные handlers, cancellation и согласованные stream
semantics не зависят от carrier-а.

#### TLS/mTLS и security profiles

`pluginprotocol` реализует TLS/mTLS, проверку цепочки/срока/назначения
сертификата, peer identity и revocation. Plugin выбирает security profile и
передаёт credential source библиотеке; product-код не создаёт TLS listener,
client или handshake самостоятельно. Для remote peer-соединений и любого
production-профиля обязателен mTLS. Явный plaintext допускается только для TCP
loopback в development; он не шифрует соединение и не удостоверяет peer. QUIC
всегда использует TLS 1.3 с взаимной аутентификацией; Unix socket и Windows
named pipe в v2 требуют mTLS независимо от локального размещения. Ошибка
защищённого соединения никогда не включает fallback на plaintext.

Для TCP, Unix socket и named pipe TLS оборачивает установленное
stream-соединение; QUIC использует собственный TLS handshake. Каждая сторона
проверяет ожидаемую peer identity. Успешного локального socket/pipe connect
недостаточно для авторизации peer.

`pluginprotocol` реализует TLS/mTLS handshake и проверки, но не выпускает и не
подписывает сертификаты, не является CA и не управляет rotation. Сертификаты,
private keys и trust roots предоставляются внешним credential provider-ом и
передаются библиотеке через её security configuration. Межплагинные trust roots
остаются отдельными от Core↔plugin REST trust roots. Неуспешная TLS-проверка,
отозванный сертификат, отсутствующие credentials либо невозможность проверить
peer identity завершают handshake fail-closed; отключение проверки и переход на
plaintext запрещены для production и remote-соединений. Явный TCP-loopback
plaintext profile разрешён только для development и не даёт peer identity;
он не применяется к QUIC или локальным IPC. OS permissions/ACL усиливают
ограничение доступа, но не заменяют mTLS для production/remote и не разрешают
plaintext fallback.

#### Endpoint и platform requirements

Каждый endpoint обязан однозначно задавать carrier и адрес; endpoint одного
carrier-а нельзя трактовать как endpoint другого. Точный URI/JSON формат,
экранирование Windows pipe names, ограничения длины и правила нормализации
определяются owner-контрактом `pluginprotocol` до реализации, а не
заимствуются из Core или продуктовых schemas.

Unix socket adapter обязан безопасно обрабатывать путь и права доступа: не
подключаться к неожиданному типу файловой системы, не удалять чужой или
подменённый socket path при старте/остановке и применять явно заданные owner и
mode. Named-pipe adapter обязан применять ограничительный ACL к разрешённым
service identities и не открывать pipe для произвольных локальных principals.
Платформенный код изолируется в infrastructure adapters; общие модели и
публичная регистрация методов не зависят от ОС. Реализацию Windows named pipes
нужно собирать и проверять на Windows; эмуляция Windows-семантики на macOS или
Linux не считается conformance. Реализации могут использовать Go `net.Conn`
abstraction и платформенный adapter, например
[`go-winio`](https://github.com/microsoft/go-winio); TLS API Go принимает
существующие stream connections через [`crypto/tls`](https://pkg.go.dev/crypto/tls).

#### Обязательный v2 conformance gate

Для каждого carrier-а одна и та же generic conformance suite проверяет
registered unary methods и bidirectional streams, peer identity, mTLS
успех/отказ, недоверенный и отозванный сертификаты, deadlines, cancellation,
concurrency, backpressure, graceful close и отсутствие fallback/downgrade.
Дополнительно проверяются carrier-specific условия:

- TCP и QUIC: адресация между hosts, TLS identity и сетевые ошибки;
- Unix domain socket: запуск/остановка, permissions, stale path, path
  substitution и отказ в доступе неразрешённому OS user;
- Windows named pipe: создающийся/закрывающийся pipe, ACL, конкурентные clients,
  отказ неразрешённому Windows principal и поведение при рестарте server process.

CI обязана выполнять реальные Windows named-pipe tests на Windows runner и
Unix-socket tests на macOS/Linux runners. v2 gate считается пройденным только
при одинаковых прикладных semantics, успешных platform-specific security tests
и отсутствии transport fallback; наличие интерфейса или unit-тестов на
поддельном listener-е недостаточно. Детальные wire/API изменения и
исполняемые vectors принадлежат только `pluginprotocol` и описываются в его
TODO до начала v2.

### Межъязыковой доступ через C ABI в v3

В v3 Go остаётся единственной реализацией wire/session engine
`pluginprotocol`. Другие языки используют native shared library с
версионированной C ABI и FFI; независимый Python codec/session/TLS stack и
параллельные реализации wire semantics не создаются. Первый официальный
binding — Python package на `cffi`.

C ABI покрывает полный generic peer facade: создание listener/client, unary
calls и двунаправленные streams. Она принимает только C-совместимые значения,
opaque handles и length-delimited byte spans; Go pointers не пересекают
границу. Входящие requests/events выдаются через bounded poll/event queue, а
host отправляет результаты и stream frames отдельными вызовами API. Заполнение
очереди сохраняет backpressure semantics протокола; callbacks из Go goroutines
в чужие runtimes не используются. Входные bytes копируются; владение
возвращаемыми буферами и API их освобождения закрепляются в публичном C header.

TLS identity, private key и trust roots передаются как length-delimited PEM
bytes и копируются в Go-owned memory. FFI сохраняет mTLS, peer identity,
revocation, deadlines, cancellation и error classification. PEM, key bytes,
payload и внутренние Go errors не попадают в публичные ошибки или logs.
`pluginprotocol` остаётся только plugin↔plugin API: Core↔plugin `Reload`, config
pull, health и metrics в C ABI не входят и принадлежат Plugin SDK.

C ABI version независима от wire contract `liapoldus.peer.v1`: additive symbols
допустимы внутри ABI-major, breaking ABI требует нового ABI-major. Публичные C
header, exported symbols, ownership rules и ABI version принадлежат
`pluginprotocol`. Python binding не является второй protocol implementation;
прочие языки считаются поддержанными только после собственного binding и
conformance.

CI выпускает native library artifacts и Python wheels с библиотекой внутри для
Linux amd64/arm64, macOS arm64 и Windows amd64. Python package не собирает Go
library при установке. Для каждой пары OS/architecture CI выполняет native ABI
smoke и protocol conformance; одних cross-build результатов недостаточно.
Shared suite проверяет полную facade поверхность, event queue bounds и
backpressure, memory ownership, cancellation, deadlines, error mapping, mTLS и
Go↔Python FFI peers в обоих направлениях для unary и streams. Поддерживаемые
версии CPython фиксируются при реализации binding, не меняя wire contract.
Native shared builds используют поддерживаемые Go build modes и cgo; конкретная
матрица подтверждается для закреплённого Go toolchain и запускается в native CI
([Go build modes](https://go.dev/src/cmd/dist/test.go), [cgo](https://pkg.go.dev/cmd/cgo)).
