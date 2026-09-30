# Плагинные библиотеки и граница взаимодействия

В системе есть две независимые Go-библиотеки с разными назначениями. Общий
Core↔plugin lifecycle принадлежит Plugin SDK: в v1 связь REST-only, в v2
доступны REST и in-process adapters с общей семантикой. Универсальная
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
| Plugin SDK, отдельный Go-модуль | В v1 — общий plugin REST server/client, служебные endpoints, bootstrap, health/readiness, settings schema discovery, exact config pull, `Reload`, базовые метрики, структурированные логи и безопасные ошибки. В v2 — тот же lifecycle contract через REST- или in-process adapter. | Межплагинный transport, Core Management API operations и продуктовые capabilities. |
| `pluginprotocol` | Generic plugin-to-plugin communication: регистрация пользовательских методов/handlers, connect/listen, unary calls и streams, физические transport/security providers. | Core lifecycle REST, config distribution, Manifest/settings/admin surfaces, готовые product RPC или имена plugin methods. |

Plugin SDK и `pluginprotocol` не импортируют друг друга. Плагин использует одну
библиотеку, обе или ни одну — в зависимости от того, нужен ли ему общий REST
lifecycle и/или прямое взаимодействие с другими плагинами. Core использует
Plugin SDK: в v1 — REST client, в v2 — явно выбранный REST либо in-process
adapter. Core не импортирует `pluginprotocol`.

Локальное расположение будущего модуля согласовано как соседний каталог
`plugin-sdk/` workspace. Canonical Go module path и Git remote не назначены;
до их решения модуль не публикуется и публичные import paths не объявляются.

## Plugin SDK: REST lifecycle v1

Каждый plugin предоставляет защищённый control endpoint по общему REST
контракту SDK. Core адресует каждую зарегистрированную replica отдельно;
Core↔plugin REST использует mTLS и уникальную identity каждой replica. В v1
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

## Целевое расширение v2: Plugin SDK без внутреннего REST

В v2 Plugin SDK должен поддерживать один transport-independent lifecycle
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

V2 implementation gate: один и тот же SDK conformance corpus проходит через
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
identity и разрешённый carrier. Для удалённых workloads применяется
аутентифицированное шифрованное соединение; отсутствие encryption или
неизвестный транспортный профиль не может стать silent downgrade. Исключение
для явно включённого loopback development profile относится только к v1 и не
переносится на v2: для всех v2 carriers, включая локальные IPC, mTLS обязателен.
Межплагинный trust не разделяет trust roots с Core REST.

В v1 Core не хранит и не распространяет caller→target/method/transport
policies: вызывающий plugin владеет своей authorization policy и передаёт её
consumer-у. Централизованное управление peer policies отложено до v2. Core не
является CA и не стоит между peers как data proxy. `pluginprotocol` не выдаёт
Core settings, не делает config pull и не предоставляет `Reload`.

V1 secret grants относятся только к раскрытию opaque secret references из
plugin settings и выдаются через Plugin SDK REST по его owner contract. Они не
авторизуют межплагинные вызовы. Call-scoped plugin-to-plugin grants и их
централизованная выдача относятся к v2; `pluginprotocol` не выпускает,
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

### Целевое расширение v2: локальные IPC carriers

Поддержка локального IPC — задача v2. Она не входит в v1 acceptance и не должна
добавляться в v1 как скрытый fallback или предварительный кодовый путь. До
открытия v2 реализация и контракты v1 не меняются.

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

#### Единая модель mTLS

mTLS обязателен для всех четырёх carrier-ов, в том числе для Unix domain socket
и Windows named pipe. Для TCP, Unix socket и named pipe TLS оборачивает
установленное stream-соединение; QUIC использует собственный TLS handshake.
Каждая сторона проверяет цепочку доверия, срок действия, назначение сертификата
и ожидаемую peer identity. Идентичность привязывается к однозначному
сертификатному идентификатору plugin replica; успешного локального socket/pipe
connect недостаточно для авторизации peer.

`pluginprotocol` реализует TLS/mTLS handshake и проверки, но не выпускает и не
подписывает сертификаты, не является CA и не управляет rotation. Сертификаты,
private keys и trust roots предоставляются внешним credential provider-ом и
передаются библиотеке через её security configuration. Межплагинные trust roots
остаются отдельными от Core↔plugin REST trust roots. Неуспешная TLS-проверка,
отозванный сертификат, отсутствующие credentials либо невозможность проверить
peer identity завершают handshake fail-closed; отключение проверки и переход на
plaintext запрещены. OS permissions/ACL усиливают ограничение доступа, но не
заменяют mTLS и не дают исключения из этого правила.

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

### Межъязыковые реализации в v2

В v2 `pluginprotocol` становится переносимым wire-протоколом, а не Go API,
которое другие языки должны пытаться вызывать напрямую. Go остаётся одной из
реализаций. Независимая реализация на другом языке должна использовать тот же
wire contract и проходить общую conformance suite; несовместимые языковые
варианты протокола не допускаются.

Для текущего wire contract источник истины — `proto/liapoldus/peer/v1/peer.proto`
в репозитории `pluginprotocol`. Он задаёт protobuf-типы только generic peer
сообщений и не задаёт product methods. Поверх transport byte stream действует
общий framing: 13-byte header — 1 byte frame type, 8-byte unsigned stream ID
в big-endian и 4-byte unsigned body length в big-endian; за ним идёт protobuf
body соответствующего frame type. Текущие frame types, их числовые значения,
stream scope, protobuf message mapping, ограничения длины и состояния сессии
должны быть перенесены из реализации в нормативную language-neutral
спецификацию и не могут определяться только Go constants или комментариями.
Эти значения нельзя менять в рамках кросс-языкового порта. Любое несовместимое
изменение wire semantics требует отдельной protocol-versioning процедуры;
переход системы к v2 сам по себе не переименовывает `liapoldus.peer.v1`.

Каждый язык генерирует свои protobuf types из общей `.proto`; generated code не
редактируется вручную и не является самостоятельной спецификацией. Кроме
generated messages реализация обязана предоставить собственные корректные:

- frame encoder/decoder с проверками unknown type, truncation, stream ID,
  body length и configured limits до выделения больших буферов;
- session engine для concurrent unary calls и multiplexed bidirectional
  streams, включая stream open/ack, data, half-close, terminal status, ping,
  close, cancellation, deadlines и backpressure;
- carrier adapters для объявленных платформой TCP, QUIC, Unix domain socket и
  Windows named pipe;
- TLS/mTLS adapter с той же проверкой trust roots, replica identity,
  certificate purpose и revocation; локальный carrier не ослабляет правило;
- idiomatic public SDK facade, сохраняющий одинаковые observable semantics.

Четырёхслойная ответственность остаётся обязательной, но физическая структура
пакетов может следовать idioms языка: transport-independent models/interfaces;
registration/session use cases; framing, carriers и crypto adapters; публичный
library facade. Generated protobuf types, OS-specific APIs и конкретные carrier
dependencies не должны проникать в application/domain API. Слои не создают
копии product schemas: method name и payload остаются opaque для библиотеки и
определяются конкретным plugin contract.

#### Межъязыковые векторы и проверка совместимости

Conformance suite должна иметь машиночитаемый corpus, независимый от Go
implementation. Он покрывает:

- валидные framing/protobuf-векторы каждого frame type и boundary размеров;
- malformed/truncated header и body, неизвестные frame types, недопустимые
  stream IDs, oversized payload и некорректные protobuf bodies;
- последовательности unary call и bidirectional stream, включая конкурентные
  stream IDs, half-close, remote/local cancellation, deadlines и terminal errors;
- TLS/mTLS handshake, ожидаемую identity, недоверенный/просроченный/отозванный
  сертификат и обязательный отказ без downgrade;
- carrier-specific permissions/ACL и platform lifecycle согласно требованиям
  выше.

Каждая реализация должна выполнить один и тот же corpus локально, а pairwise
interop gate должен запускать реального child-process peer на каждом
поддерживаемом языке: Go↔другой язык в обе стороны для unary и stream, а также
межъязыковые negative/security сценарии. Совпадение только unit-тестов внутри
одной реализации или cross-build без запуска не доказывает interop. Ожидаемый
результат corpus нельзя генерировать и проверять только одной реализацией;
vectors ревьюируются как contract assets, а изменения wire-поведения — как
явное изменение версии/контракта.

Первый non-Go target v2 уже выбран: **Python**. Владелец protocol добавляет
изолированный Python package/SDK к репозиторию `pluginprotocol`, не смешивая его
с Go module и сохраняя один source of truth wire-контрактов. Python API должен
быть idiomatic для языка, но не менять wire semantics, error mapping,
authorization boundary или mTLS requirements. Публичные product methods и
schemas в SDK не добавляются. До начала реализации отдельно фиксируются
поддерживаемые CPython versions, packaging/dependency policy и конкретные
security-reviewed carrier libraries; выбор реализации не меняет протокол.

Python SDK проходит тот же corpus и полный заявленный carrier/platform matrix,
включая Windows named pipes на Windows runner, а также двунаправленный реальный
Go↔Python interop для unary и streams. CI должна запускать Python-версии с
объявленной матрицей на поддерживаемых ОС; cross-build или вызов Go через
subprocess wrapper не считается Python implementation. Добавление каждого
следующего языка — отдельное решение и gate; документация не заявляет его
поддержку до прохождения pairwise interop.
