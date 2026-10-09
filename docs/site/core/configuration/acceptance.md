# Критерии готовности Core v1

Страница задаёт обязательные acceptance gates. Она не является журналом
прошлых запусков: gate считается пройденным только при наличии свежего
воспроизводимого теста и результата на текущем дереве. Детали контрактов
находятся в [целевой архитектуре](../architecture/target), [Control Plane](../architecture/control-plane),
[REST API](../api/) и документации владельцев компонентов.

## Блокирующие gates

| Область | Условие PASS |
| --- | --- |
| Первый сквозной путь | Один интеграционный E2E с тремя отдельно запущенными сервисами проходит цепочку `Management PUT → SQLite active/previous → Core REST Reload → Plugin SDK mTLS exact-generation pull → Server-owned schema validation and atomic apply → digest ACK → обработанный HTTP traffic`; отдельно тот же E2E доказывает разрешённый прямой `Server → forms-db` submit по `pluginprotocol` и mTLS без Core в data path. SQL-вариант того же production пути проверяет 24 параллельные формы и bounded cursor pagination на PostgreSQL, MySQL и MariaDB. Core сам не запускает тестовые или production plugin processes. Ошибка pull/apply не активирует некорректный документ; конфигурация и runtime остаются согласованы по roll-forward policy. |
| Raw settings | `PUT` принимает plugin JSON object напрямую; Core сохраняет исходные UTF-8 bytes в `plugin_config_generations.raw_json BLOB`, вычисляет digest по ним и отдаёт при exact-generation pull те же bytes. Проверены duplicate keys, invalid UTF-8/JSON, size/schema rejection и отсутствие изменения active generation/Reload при ошибке. |
| Config lifecycle | На instance используются durable slots `active`, `previous` и внутренний `staging`; только первые два доступны plugin config pull. Candidate staging, promotion-before-Reload, roll-forward, per-replica fencing, rollback, idempotency и crash recovery подтверждаются integration tests. |
| Plugin SDK | Независимый четырёхслойный Go SDK обслуживает общий REST lifecycle, production per-replica mTLS, Reload, exact-generation config pull, health/readiness, error redaction, JSON stdout logs и Prometheus scrape. SDK WIP реализует выключенный по умолчанию opt-in loopback plaintext listener только для generic `GET /_liapoldus/v1/health`; Core пока закреплён на опубликованной SDK revision без этого v2 profile, поэтому он не считается доступным в Core release. `/ready` остаётся mTLS-only из-за identity/generation полей; config pull и secret grants всегда требуют mTLS. Rollback — Core Management API operation, после смены slots Core вызывает обычный Reload; plugin-side rollback endpoint отсутствует. Тестовый harness самостоятельно запускает процессы и не использует Core process management. |
| Core composition | Core импортирует только REST Plugin SDK для lifecycle; не импортирует `pluginprotocol`, не содержит plugin/product-specific ветвлений и строит immutable in-memory snapshot до готовности. |
| Plugin operation | Оператор выбранными средствами устанавливает и запускает Core, Server plugin и forms-db plugin. Caddy — внутренняя реализация Server plugin. Replicas регистрируются через Plugin SDK mTLS и удерживают membership leases; Core не выполняет install/start/stop/restart/scale/delete ни в одной версии. Deployment profiles подтверждаются отдельными native smoke gates. |
| Peer networking | `pluginprotocol` — generic peer-only library в четырёх слоях; собственные plugin methods регистрируются вызывающим plugin. TCP/QUIC и security profile выбираются без изменения application API; remote/production требует mTLS, а явный plaintext разрешён только для TCP loopback development без fallback. |
| Security | Management API, per-replica REST mTLS, peer-network trust roots и grants разделены. Проверены identity mismatch, revocation, secret redaction, authorization, audit и отсутствие credentials/secrets в API, logs, errors и durable config. |
| Server plugin | HTTP/1.1, HTTP/2/3, TLS/ACME, static, proxy/upstream pools, WebSocket, SSE, plugin dispatch, site manifest/artifact, `current`/`previous` и restart recovery проходят plugin-owned conformance. Caddy-L4, TCP/UDP listeners и relay не входят в v1/v2 scope. Ни одного Caddy runtime/data-plane кода в Core. |
| Product plugins | `forms-db` соответствует собственным схемам и runtime tests; общий lifecycle использует Plugin SDK. `captcha` и `identity` полностью заморожены и исключены из v1/v2 acceptance. Core остаётся product-agnostic. |
| Durable recovery | SQLite, immutable in-memory snapshot, operation journal, replica ACK и слоты `active`/`previous`/`staging` согласуются после каждого crash point; оператор отдельно восстанавливает plugin-owned persistent data по runbook. |
| SQLite backup/restore | `database backup` создаёт новый файл через online snapshot и проверяет schema version, `quick_check(1)`, foreign keys и права `0600`; overwrite запрещён. `database restore` валидирует файл, атомарно заменяет существующий SQLite state и отказывает при удерживаемом Core state lock. Восстановление сохраняет конфигурационные bytes и не включает plugin-owned data или secret bytes. |
| API and documentation | OpenAPI, versioned contracts, generated mirrors, CLI, security и recovery docs совпадают с runtime. Нет активной документации для удалённых lifecycle/API и нет дублирующих исторических acceptance summaries. |
| Platform gate | VitePress build; `core`: `make check`, `go vet ./...`, `make staticcheck-u1000`, macOS/Linux builds; `plugin-sdk`: tests, `go build`, `go vet`; `pluginprotocol`: `make check`, `go build`, `go vet`; активные plugins (`server`, `forms-db`): contract suite, `go test ./...`, `go build ./...`, `go vet ./...`; smoke вручную размещённых plugin endpoints на поддерживаемых ОС. Caddy-L4 и Core process/workload management не входят в v1. |

## Рабочее evidence

У каждой строки implementation owner хранит исполняемые тесты и команды в своём
`TODO.md`. После завершения slice он обновляет соответствующий TODO и ссылку
здесь на тест/команду. Числа тестов и старые результаты в этой странице не
копируются. Пока все обязательные gates выше не имеют свежего PASS, Core v1
не считается готовым или production-ready.

## Рамки

- CAPTCHA, Identity/OIDC/OAuth и их продуктовые реализации отложены до v3;
  их репозитории не входят в active workspace/v1/v2 gates. Это не исключает обязательную
  authentication/authorization, mTLS, аудит и redaction самого Management API.
- `archive/plugins/tls-issuer/` и `test/` не входят в runtime workspace.
- Реализация не зависит от настроенного Git remote. Canonical SDK module path
  утверждён как `github.com/Liapoldus/plugin-sdk`; неизвестные URLs других
  repos не угадываются, а публикация требует отдельного запроса.
- PostgreSQL/S3 не входят в Core v1; forms-db product storage — отдельный scope.
  Multi-Core deployment не входит в v1.
