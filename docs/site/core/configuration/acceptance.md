# Критерии готовности Core v3

Страница задаёт обязательные acceptance gates. Она не является журналом
прошлых запусков: gate считается пройденным только при наличии свежего
воспроизводимого теста и результата на текущем дереве. Детали контрактов
находятся в [целевой архитектуре](../architecture/target), [Control Plane](../architecture/control-plane),
[REST API](../api/) и документации владельцев компонентов.

## Блокирующие gates

| Область | Условие PASS |
| --- | --- |
| Первый сквозной путь | Один E2E с отдельно запущенными сервисами проходит цепочку `Management PUT → SQLite active/previous → REST+mTLS Reload → Plugin SDK exact-generation pull → schema validation and atomic apply → digest ACK → traffic`; отдельный in-process E2E доказывает тот же результат через instance-scoped snapshot без listener. Оба пути также доказывают прямой `Server → forms-db` submit по `pluginprotocol` и mTLS без Core в data path. Core сам не запускает test или production plugin processes. Ошибка pull/apply не активирует некорректный документ; конфигурация и runtime остаются согласованы по roll-forward policy. |
| Raw settings | `PUT` принимает plugin JSON object напрямую; Core сохраняет исходные UTF-8 bytes в `plugin_config_generations.raw_json BLOB`, вычисляет digest по ним и отдаёт при exact-generation pull те же bytes. Проверены duplicate keys, invalid UTF-8/JSON, size/schema rejection и отсутствие изменения active generation/Reload при ошибке. |
| Config lifecycle | На instance используются durable slots `active`, `previous` и внутренний `staging`; только первые два доступны plugin config pull. Candidate staging, promotion-before-Reload, roll-forward, per-replica fencing, rollback, idempotency и crash recovery подтверждаются integration tests. |
| Plugin SDK | Независимый четырёхслойный Go SDK обслуживает REST+mTLS lifecycle для отдельных процессов и in-process lifecycle для доверенных Go plugins; обе поверхности используют одинаковые Reload, exact-generation pull, ACK, readiness, error redaction, JSON logs и Prometheus semantics. Loopback plaintext остаётся только явным dev health-профилем; config pull и secret grants всегда требуют production mTLS в REST режиме. Rollback — Core Management API operation, после смены slots Core вызывает обычный Reload; plugin-side rollback endpoint отсутствует. Тестовый harness самостоятельно запускает процессы и не использует Core process management. |
| Core composition | Core использует два явных lifecycle adapters с одним контрактом, не импортирует `pluginprotocol` для lifecycle, не содержит plugin/product-specific ветвлений и строит immutable in-memory snapshot до готовности. Автоматического fallback между adapters нет. |
| Plugin operation | Оператор выбранными средствами устанавливает и запускает Core, Server plugin и forms-db plugin. Caddy — внутренняя реализация Server plugin. Replicas регистрируются через Plugin SDK mTLS и удерживают membership leases; Core не выполняет install/start/stop/restart/scale/delete ни в одной версии. Deployment profiles подтверждаются отдельными native smoke gates. |
| Peer networking | `pluginprotocol` — generic peer-only library в четырёх слоях; собственные plugin methods регистрируются вызывающим plugin. TCP/QUIC и security profile выбираются без изменения application API; remote/production требует mTLS, а явный plaintext разрешён только для TCP loopback development без fallback. |
| Security | Management API, per-replica REST mTLS, peer-network trust roots и grants разделены. Проверены identity mismatch, revocation, secret redaction, authorization, audit и отсутствие credentials/secrets в API, logs, errors и durable config. |
| Server plugin | HTTP/1.1, HTTP/2/3, TLS/ACME, static, proxy/upstream pools, WebSocket, SSE, plugin dispatch, site manifest/artifact, `current`/`previous` и restart recovery проходят plugin-owned conformance. Caddy-L4, TCP/UDP listeners и relay не входят в текущий Core scope. Ни одного Caddy runtime/data-plane кода в Core. |
| Product plugins | `forms-db` соответствует собственным схемам и runtime tests; общий lifecycle использует Plugin SDK. `captcha` и `identity` полностью заморожены и исключены из v3 acceptance. Core остаётся product-agnostic. |
| Durable recovery | SQLite, immutable in-memory snapshot, operation journal, replica ACK и слоты `active`/`previous`/`staging` согласуются после каждого crash point; оператор отдельно восстанавливает plugin-owned persistent data по runbook. |
| SQLite state ownership | Core владеет SQLite schema, integrity gates и exclusive state lock. Backup/restore, как и deploy/lifecycle, выполняются универсальным CLI из отдельного репозитория; Core не содержит этих команд и не принимает file-maintenance API. |
| API and documentation | OpenAPI, versioned bundle/apply contracts, generated mirrors, standalone CLI, security и recovery docs совпадают с runtime. В Core нет активной user-facing CLI surface и нет дублирующих исторических acceptance summaries. |
| Platform gate | VitePress build; `core`: `make check`, `go vet ./...`, race и Linux/macOS/Windows builds; `plugin-sdk`: tests, race, `go build`, `go vet`; `pluginprotocol`: `make check`, race, `go build`, `go vet` и native TCP/QUIC/Unix/named-pipe checks; активные plugins (`server`, `forms-db`): contract suite, `go test ./...`, `go build ./...`, `go vet ./...`; smoke вручную размещённых plugin endpoints на поддерживаемых ОС. Release workflow обязан публиковать checksum, подписанный artifact, SBOM и provenance. Caddy-L4 и Core process/workload management не входят в Core scope. |

## Рабочее evidence

У каждой строки implementation owner хранит исполняемые тесты и команды в своём
`TODO.md`. После завершения slice он обновляет соответствующий TODO и ссылку
здесь на тест/команду. Числа тестов и старые результаты в этой странице не
копируются. Пока все обязательные gates выше не имеют свежего PASS, Core v3
не считается готовым или production-ready.

## Рамки

- CAPTCHA, Identity/OIDC/OAuth и их продуктовые реализации остаются вне этого
  release train; их репозитории не входят в active workspace и v3 gates. Это не исключает обязательную
  authentication/authorization, mTLS, аудит и redaction самого Management API.
- `archive/plugins/tls-issuer/` и `test/` не входят в runtime workspace.
- Реализация не зависит от настроенного Git remote. Canonical SDK module path
  утверждён как `github.com/Liapoldus/plugin-sdk/v2`; неизвестные URLs других
  repos не угадываются, а публикация требует отдельного запроса.
- PostgreSQL/S3 не входят в Core; forms-db product storage — отдельный scope.
  Multi-Core deployment не входит в текущий v2 scope.
