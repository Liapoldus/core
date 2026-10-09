# Конфигурация Core

Core читает при запуске только bootstrap-переменную `CORE_SQLITE_PATH` и уже
инициализированные настройки из SQLite. Environment не создаёт и не
переопределяет сохранённые значения. Plugin settings изменяются через
Management API с CAS/ETag и audit.

| Документ | Назначение |
| --- | --- |
| [Bootstrap и запуск](bootstrap) | SQLite state и источник настроек при старте. |
| [Plugin configuration API](/core/api/config) | CAS, versioned JSON, REST Reload/config pull и operation lifecycle. |
| [Миграция](migration) | Внешний CLI workflow для старого YAML bootstrap. |
| [Безопасность](security) | Раздельные REST и peer-network identities, grants и redaction. |
| [Транспорты](transports) | Трафик, который обслуживает отдельный Server plugin. |
| [Каталог ошибок](errors) | Публичные safe errors и problem response. |
| [Архитектура](../architecture/target) | Единственная каноническая модель состояния и владения. |
