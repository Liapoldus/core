# Конфигурация Core

У Core один источник desired-конфигурации подключённых сервисов — SQLite.
Plugin-specific JSON settings и Caddy traffic configuration изменяются через
Management API, валидируются schema подключённого plugin, затем Core вызывает
REST `Reload` и plugin pull-ит точные исходные bytes новой generation.
`core.yaml` содержит только bootstrap самого Core; маршруты и сервисные
настройки в YAML/Caddyfile не задаются.

| Документ | Назначение |
| --- | --- |
| [Bootstrap schema](bootstrap) | Минимальный `core.yaml` и startup граница. |
| [Публичная JSON Schema](/spec/core.schema.json) | Machine-readable bootstrap contract. |
| [Plugin configuration API](/core/api/config) | CAS, versioned JSON, REST Reload/config pull и operation lifecycle. |
| [Безопасность](security) | Раздельные REST и peer-network identities, grants и redaction. |
| [Транспорты](transports) | Трафик, который обслуживает отдельный Server plugin. |
| [Каталог ошибок](errors) | Публичные safe errors и problem response. |
| [Архитектура](../architecture/target) | Единственная каноническая модель состояния и владения. |
