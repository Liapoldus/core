# Сервисные библиотеки и граница взаимодействия

> Нормативным источником production-ready v3 является
> [целевая архитектура v3](v3). Core lifecycle использует только Service SDK REST.

В системе есть две независимые Go-библиотеки с разными назначениями. Общий
Core↔service lifecycle принадлежит Service SDK и использует REST+mTLS.
Универсальная
межсервисная сеть принадлежит `wool`. Ни одна из библиотек не владеет
контрактами конкретных продуктов: схемы и методы CAPTCHA, Caddy, forms-db и
identity определяются только соответствующими сервисами.

Каноническая ownership-модель приведена в [целевой архитектуре](target),
состояние Core и поколения — в [control-plane](control-plane), а фактические
блокеры и команды проверки — в [матрице приёмки](../configuration/acceptance).
Эта страница фиксирует границы, не дублируя protobuf, JSON schemas или REST
OpenAPI конкретных владельцев.

## Две независимые библиотеки

| Библиотека | Ответственность | Что не входит |
| --- | --- | --- |
| Service SDK, отдельный Go-модуль | Общий REST+mTLS server/client, bootstrap, health/readiness, settings schema discovery, exact config pull, `Reload`, метрики, структурированные логи, безопасные ошибки. | Межсервисный transport, Core Management API operations и продуктовые capabilities. |
| `wool` | Generic service-to-service communication: регистрация пользовательских методов/handlers, connect/listen, unary calls и streams, физические transport/security providers. | Core lifecycle REST, config distribution, Manifest/settings/admin surfaces, готовые product RPC или имена service methods. |

Service SDK и `wool` не импортируют друг друга. Сервис использует одну
библиотеку, обе или ни одну — в зависимости от того, нужен ли ему общий REST
lifecycle и/или прямое взаимодействие с другими сервисами. Core использует
Service SDK через REST client. Core не импортирует
`wool` для lifecycle/configuration.

TLS/mTLS реализуют библиотеки, а не конкретные сервисы: Service SDK владеет
Core↔service REST security, `wool` — security peer carriers. Service
передаёт нужной библиотеке bootstrap security configuration и credential source
через её API; product handlers не создают собственный TLS stack, не выполняют
handshake и не повторяют проверку сертификатов. Trust roots этих двух каналов
раздельны. Security profile выбирается в конфигурации соответствующей
библиотеки, отдельно от product settings; service только передаёт профиль и
credential source её API. Production и удалённые Core↔service REST connections
используют per-replica mTLS по versioned SDK v2 HTTP contract. SDK реализует
отдельный versioned loopback-only plaintext development profile: он opt-in,
выключен по умолчанию и на отдельном literal-loopback TCP listener обслуживает
только generic `GET /healthz`. `/ready` остаётся mTLS-only из-за
identity/generation полей ответа; redacted readiness view потребует отдельного
versioned contract. Exact config pull и secret-grant обмен всегда используют
mTLS. Профиль не является production transport и не используется как secure
fallback. Peer protocol
уже допускает явный TCP-loopback plaintext в development без encryption и peer
identity. QUIC всегда зашифрован и аутентифицирован; v3 Unix socket/named pipe
пока требуют mTLS. Ни один профиль не переключается автоматически после ошибки
TLS.

Локальное расположение модуля — соседний каталог `service-sdk/` workspace.
Утверждённый canonical Go module path — `github.com/Liapoldus/service-sdk/v2`;
все active consumers используют этот major path.

## Service SDK: REST lifecycle

Каждый service предоставляет защищённый control endpoint по общему REST
контракту SDK. Core адресует каждую зарегистрированную replica отдельно;
Core↔service REST использует mTLS и уникальную identity каждой replica. В Core v3
оператор выдаёт credentials через внешний CA/PEM source; Core не выпускает
identity и не внедряет её через process/container bootstrap. Trust roots control
plane отделены от service-to-service trust roots. Plaintext или bearer-only
downgrade не допускается.
Endpoint доступен только Core management identity и не является пользовательским
traffic API.

SDK contract source — `service-sdk/infrastructure/assets/service-sdk/v2/http-contract.json`.
В нём заданы service-side endpoints:

- bootstrap identity/version, Manifest и settings schema;
- health и readiness;
- `POST /_liapoldus/v1/reload` с generation, digest и schema version;
- exact-generation pull: service делает `GET /internal/v1/service-config/{generation}` к Core;
- readiness/ACK и bounded REST errors; запуск, остановка и drain процесса
  выполняются оператором вне Core;
- общих метрик, структурированных логов и безопасных error/problem responses.

Core сначала сохраняет исходный JSON в SQLite и продвигает active generation,
затем вызывает `Reload` без передачи документа. Config pull доступен только
по per-replica mTLS и возвращает точные исходные bytes; service валидирует и
применяет их в памяти, после чего ACK-ает generation/digest. Слоты
`active`/`previous`, roll-forward при частичном результате, rollback,
idempotency, retries и fencing нормативно определены в
[control-plane](control-plane). REST endpoint shapes и wire JSON принадлежат
Service SDK contract.

SDK даёт общие primitives, но не выбирает product settings и не создаёт
необязательные настройки за service. Service сам владеет versioned JSON Schema,
значениями полей, validation и runtime application. Общие логи не должны
содержать settings, cookies, credentials, tokens, private keys, grant handles
или secret bytes. Непредвиденная ошибка возвращается как безопасный типовой
problem, а диагностические детали остаются в redacted server-side logs.

## Core → Service SDK REST

Control plane: Studio → CLI → Core → Service SDK REST+mTLS.
Сервис получает `Reload(generation)` и pull-ит точные immutable bytes через SDK.
Независимые Services используют Wool для data plane. StudioPlugins — только
расширения редактора.

## `wool`: generic service-to-service network

`wool` — самостоятельная Go-библиотека для прямого обмена между
сервисами. В ней нет predeclared бизнес-методов: service регистрирует собственные
имена методов и обработчики, а peer вызывает их по общему transport API.
Библиотека не должна знать, какие продукты существуют, каковы их capability
names, какие у них settings, кто является «Caddy» или «identity», и как
выглядят их payloads.

Прикладной метод и его request/response schema принадлежат сервису, который его
объявил. `wool` предоставляет только общую оболочку вызова и
транспортные примитивы. Одни и те же application-level method names, payloads,
ошибки и stream semantics должны сохраняться при смене физического carrier-а.
Конкретные версии транспорта и security profiles закрепляются в owner-контракте
самого `wool` после conformance-проверок; эта страница не фиксирует
непринятый wire format или API signatures.

До открытия соединения библиотека проверяет общий peer policy/configuration,
identity и разрешённый carrier. Для удалённых workloads и production применяется
аутентифицированное шифрованное соединение; отсутствие encryption или
неизвестный транспортный профиль не может стать silent downgrade. Явный
loopback TCP plaintext development profile допускается также в v3: он не
аутентифицирует peer и не может обслуживать удалённый адрес. QUIC остаётся
зашифрованным и аутентифицированным, а локальные IPC используют mTLS. SDK
loopback plaintext development profile для Core REST — отдельное целевое
расширение, не входящее в текущий production contract. Межсервисный trust не разделяет
trust roots с Core REST.

В Core v3 caller→target/method/transport policies хранятся в Core SQLite,
авторизуются через Management API и распространяются через SDK-defined
directory. Service не владеет центральной policy и получает только разрешённую
выборку для своей replica. `wool` исполняет выбранный carrier.
`wool` только исполняет выбранный разрешённый carrier и не получает
Core policy storage/API. Core не является CA и не стоит между peers как data
proxy. `wool` не выдаёт Core settings, не делает config pull и не
предоставляет `Reload`.

Secret grants относятся только к раскрытию opaque secret references из
service settings и выдаются через Service SDK REST по его owner contract. Они не
авторизуют межсервисные вызовы. Call-scoped service-to-service grants и их
централизованная выдача относятся к следующему этапу; `wool` не выпускает,
валидирует или погашает grants.

## Физический transport и безопасность

Физический канал настраивается отдельно от прикладных методов. Service выбирает
поддержанный library carrier и security provider в runtime bootstrap, сохраняя
зарегистрированные handlers и payload contracts. Добавление carrier-а или
изменение listen/dial-параметров не должно требовать переписывать product
handlers.

Окончательный carrier matrix, поля профиля, timeout/backpressure defaults,
credential providers, rotation и revocation должны быть нормативно описаны в
`wool` и покрыты conformance до production реализации. Уже принятое
требование: удалённая связь защищена и аутентифицирует peer; Core REST и
service-to-service connections используют разные identities и trust roots.

Конкретный transport SDK организуется четырьмя слоями `domain`, `application`,
`infrastructure`, `presentation` согласно `wool/AGENTS.md`. Domain
содержит только модели и interfaces; carrier, криптография, sockets и TLS
реализуются infrastructure adapters; presentation экспонирует публичный
library facade. Product contracts в этот модуль не переносятся.

### Локальные IPC carriers

Локальный IPC является частью v3 carrier matrix. Он выбирается явно и не
является скрытым fallback или downgrade после ошибки другого транспорта.

В v3 `wool` предоставляет четыре явно выбираемых carrier-а:

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

`wool` реализует TLS/mTLS, проверку цепочки/срока/назначения
сертификата, peer identity и revocation. Service выбирает security profile и
передаёт credential source библиотеке; product-код не создаёт TLS listener,
client или handshake самостоятельно. Для remote peer-соединений и любого
production-профиля обязателен mTLS. Явный plaintext допускается только для TCP
loopback в development; он не шифрует соединение и не удостоверяет peer. QUIC
всегда использует TLS 1.3 с взаимной аутентификацией; Unix socket и Windows
named pipe в v3 требуют mTLS независимо от локального размещения. Ошибка
защищённого соединения никогда не включает fallback на plaintext.

Для TCP, Unix socket и named pipe TLS оборачивает установленное
stream-соединение; QUIC использует собственный TLS handshake. Каждая сторона
проверяет ожидаемую peer identity. Успешного локального socket/pipe connect
недостаточно для авторизации peer.

`wool` реализует TLS/mTLS handshake и проверки, но не выпускает и не
подписывает сертификаты, не является CA и не управляет rotation. Сертификаты,
private keys и trust roots предоставляются внешним credential provider-ом и
передаются библиотеке через её security configuration. Межсервисные trust roots
остаются отдельными от Core↔service REST trust roots. Неуспешная TLS-проверка,
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
определяются owner-контрактом `wool` до реализации, а не
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
исполняемые vectors принадлежат только `wool` и описываются в его
TODO до начала v2.

### Foreign bindings

Foreign bindings, C ABI и отдельные language engines в v3 не входят. Поддержанная
surface ограничена официальным Go facade; расширение межъязыкового API требует
отдельного решения, нового контракта и собственного conformance gate.
