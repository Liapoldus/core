# Кодовая архитектура Core

Целевая структура Core отражает его роль runtime control plane: composition root,
плоские application use cases, domain-only models/interfaces, технические
infrastructure adapters и входной Management API. Универсальный `liapoldus` CLI
является отдельным репозиторием и клиентом API. Caddy runtime и public
traffic handlers принадлежат отдельному `plugins/server` и не входят в Core.
Plugin protocol generated types, SQLite driver, filesystem и TLS SDK не
проникают в domain models или public API DTO.

## Целевые Core adapters

- ENV bootstrap и SQLite для собственных настроек Core; первый запуск один раз
  consumes `CORE_INIT_*` до открытия Management API;
- SQLite migrations/repositories для generic plugin instances, JSON config
  generations (`active`/`previous` и internal `staging`), per-replica
  endpoints/identity references, operations, idempotency, access и audit;
- Plugin SDK REST client для заранее вручную запущенных plugin replicas,
  scoped secret grants и immutable in-memory
  desired/applied generations;
- Management REST API для plugin-neutral lifecycle, settings, health, operations,
  access и audit. CLI, Git и deployment adapters сюда не входят.

Core не содержит Caddy build/runtime adapter, Caddy Admin pass-through,
route/group compiler, Caddy-specific API, public HTTP listener или traffic
proxy. `plugins/server` сам владеет Caddy и вызывает другие plugin
instances через разрешённые plugin-to-plugin peer connections. Core не импортирует
`pluginprotocol`; он использует только Plugin SDK REST. См.
[границы библиотек](protocol).

## Обязательная структура четырёх слоёв

В `core` разрешены ровно четыре слоя; новые слои и альтернативные корни пакетов
не вводятся.

| Слой | Разрешённое содержимое | Запрещённое содержимое |
| --- | --- | --- |
| `internal/domain` | Только `models/` и `interfaces/`. `models/` содержит модели и связанные typed errors; `interfaces/` — порты. Каждая модель, typed error и интерфейс объявляются в отдельном файле. Допустимы валидирующие конструкторы и валидация модели. | Use cases, реализации, I/O, зависимости от инфраструктуры, отдельные `types`, `errors`, `services` или другие каталоги. |
| `internal/application` | Плоский набор use cases и их orchestration-кода. | Глубокие деревья каталогов, транспортные DTO, SQL/Caddy/gRPC детали, `reflect`-диспетчеризация. |
| `internal/infrastructure` | Реализации портов, сгруппированные по техническим адаптерам в тематические подкаталоги. | Предметные правила конкретных плагинов и публичные API-типы. |
| `internal/presentation` | Только входной адаптер `api/` и `api/handlers/`. | CLI, Git, deployment adapters, хранилища, бизнес-логика и Caddy data-plane handlers. Другие корневые пакеты и произвольные вложенные package-каталоги не вводятся. |

Composition root и запуск единственного Core процесса находятся в
`cmd/core` является runtime composition root; Go unit/integration tests размещаются
рядом с кодом, а TypeScript frontend/HTTP/child-process tests — в `tests/`. SQL, сообщения и неизменяемые
определения принадлежат коду. Публичные schemas/OpenAPI переходят на generated
artifacts; в `assets/` не допускается Go-код. Plugin binaries
собираются и тестируются в собственных репозиториях; Core подключает к ним
только общие protocol interfaces.

Разрешённые внутренние границы presentation направлены только внутрь API
адаптера: `api` root может вызывать `api/handlers`, но handlers не импортируют
API root. Core bootstrap и lifecycle запускаются composition root и внешним CLI
через process/API boundary. Обратные зависимости
и циклы запрещены. Разделение файлов внутри одного пакета само по себе не
создаёт нового слоя.

### Направление зависимостей

`domain` зависит только от стандартной библиотеки и собственных моделей;
`application` — от typed domain interfaces/models; `infrastructure` реализует
порты; `presentation` переводит входные запросы в вызовы application. Только
composition root связывает реализации и входные adapters. Domain-модели и API
DTO не должны импортировать Caddy, SQLite, gRPC или filesystem packages.

### Настройки и code-owned определения

Изменяемые operational settings принадлежат SQLite Core; ENV задаёт только
bootstrap, а библиотеки получают typed параметры от composition root.
Имена полей, error codes, сообщения, постоянные лимиты и параметризованный SQL
имеют одного владельца в коде, а не отдельный runtime parser. Публичные
контракты должны генерироваться детерминированно. Переход ещё не завершён:
SQLite schema/queries и storage definitions уже перенесены в Go; остальные
loaders и runtime bootstrap остаются открытыми в TODO.

## Persistence invariants

SQLite foreign keys, migrations, write transaction boundaries и crash recovery
покрываются native Go tests и TypeScript black-box API/child-process tests. Изменение
Core desired configuration валидируется до durable metadata pointer commit;
активация plugin settings/generations ожидает точных protocol ACK. Между
SQLite и удалённым plugin нет общей ACID transaction: durable operation journal
и компенсация обязательны. Orphaned immutable files допустимы для безопасного
последующего GC; dangling metadata references недопустимы.

Подробная карта Core storage и восстановления: [Control plane и ER-модель](control-plane).
Регистрация, leases и ответственность deployment-клиента описаны в
[целевой архитектуре](target). Core не устанавливает и не супервизирует
workloads. Импортные границы и слои закрепляются architecture lint в Core.
