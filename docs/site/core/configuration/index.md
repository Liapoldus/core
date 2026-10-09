# Конфигурация Core

Core читает при запуске только bootstrap-переменную `CORE_SQLITE_PATH`. При
первом старте сам создаёт начальные настройки из `CORE_INIT_*`; затем собственные
настройки Core и подключённых plugins хранятся в SQLite. Environment больше не
переопределяет сохранённые значения. Plugin settings изменяются через
Management API с CAS/ETag и audit.

| Документ | Назначение |
| --- | --- |
| [Bootstrap и запуск](bootstrap) | ENV, auto-bootstrap Core и источник настроек при старте. |
| [Plugin configuration API](/core/api/config) | CAS, versioned JSON, REST Reload/config pull и operation lifecycle. |
| [Миграция](migration) | Офлайн-переход со старого YAML bootstrap. |
| [Безопасность](security) | Раздельные REST и peer-network identities, grants и redaction. |
| [Транспорты](transports) | Трафик, который обслуживает отдельный Server plugin. |
| [Каталог ошибок](errors) | Публичные safe errors и problem response. |
| [Архитектура](../architecture/target) | Единственная каноническая модель состояния и владения. |
