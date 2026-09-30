# Кодовая архитектура Core

Целевая структура Core отражает его роль control plane: composition root,
плоские application use cases, domain-only models/interfaces, технические
infrastructure adapters и входные Management API/CLI. Caddy runtime и public
traffic handlers принадлежат отдельному `plugins/server` и не входят в Core.
Plugin protocol generated types, SQLite driver, filesystem и TLS SDK не
проникают в domain models или public API DTO.

## Целевые Core adapters

- bootstrap loader и валидация минимального `core.yaml`;
- SQLite migrations/repositories для generic plugin instances, JSON config
  generations (`active`/`previous` и internal `staging`), per-replica
  endpoints/identity references, operations, idempotency, access и audit;
- Plugin SDK REST client для заранее вручную запущенных plugin replicas,
  scoped secret grants и immutable in-memory
  desired/applied generations;
- Management REST API и CLI для plugin-neutral lifecycle, settings,
  health, operations, access и audit.

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
| `internal/presentation` | Только входные адаптеры `api/` и `cli/`. Для группировки обработчиков и composition/lifecycle-кода разрешены подпакеты `api/handlers/`, `cli/bootstrap/` и `cli/caddyruntime/`; это части соответствующих адаптеров, а не новые архитектурные слои. | Хранилища, бизнес-логика и Caddy data-plane handlers. Другие корневые пакеты и произвольные вложенные package-каталоги не вводятся. |

Composition root и запуск единственного Core процесса находятся в
`cmd/core`; тесты хранятся отдельно в `tests/`, а не рядом с
production-пакетами. Статические контракты, схемы и прочие данные размещаются
во внешних contract assets; в `assets/` не допускается Go-код. Plugin binaries
собираются и тестируются в собственных репозиториях; Core подключает к ним
только общие protocol interfaces.

Разрешённые внутренние границы presentation направлены только внутрь адаптера:
`api` root может вызывать `api/handlers`, но handlers не импортируют API root;
CLI может использовать только предназначенные для него bootstrap/lifecycle
adapters. Caddy runtime не является подпакетом Core CLI. Обратные зависимости
и циклы запрещены. Разделение файлов внутри одного пакета само по себе не
создаёт нового слоя.

### Направление зависимостей

`domain` зависит только от стандартной библиотеки и собственных моделей;
`application` — от typed domain interfaces/models; `infrastructure` реализует
порты; `presentation` переводит входные запросы в вызовы application. Только
composition root связывает реализации и входные adapters. Domain-модели и API
DTO не должны импортировать Caddy, SQLite, gRPC или filesystem packages.

### Конфигурация вместо хардкода

Не зашивать в production-код изменяемые продуктовые значения: контрактные
строки и ключи, имена capabilities, сообщения ошибок, пути, defaults, имена
переменных окружения, флаги CLI, лимиты и значения конфигурации. Для них
используются версионированные contract assets/configuration sources; код
содержит только необходимую логику загрузки, типизации и валидации. Исключения
должны быть явно перечислены в локальном `core/AGENTS.md` и проверяться
architecture/AST gate. Эта норма не запрещает структурные Go identifiers и
алгоритмические константы, которые не являются внешним контрактом или
настраиваемым поведением.

## Persistence invariants

SQLite foreign keys, migrations, write transaction boundaries и crash recovery
покрываются standalone TypeScript tests по black-box API/CLI. Core-owned
package/artifact write проходит проверку до durable metadata pointer commit;
активация plugin settings/generations ожидает точных protocol ACK. Между
SQLite и удалённым plugin нет общей ACID transaction: durable operation journal
и компенсация обязательны. Orphaned immutable files допустимы для безопасного
последующего GC; dangling metadata references недопустимы.

Подробная карта Core storage и восстановления: [Control plane и ER-модель](control-plane).
Нормативные роли и ручной v1 startup: [целевая архитектура](target). Local
supervision и контейнерные providers — v2. Импортные
границы и слои дополнительно закрепляются architecture lint в Core.
