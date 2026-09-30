# Bootstrap `core.yaml`

`core.yaml` содержит только bootstrap самого Core: путь к SQLite,
Management listener/TLS и лимиты Management API. Plugin settings и фиксированные
endpoints/replica identities задаются через Management API и хранятся Core в
SQLite. Binary releases и provider connections в v1 Core не управляет.
Файл не задаёт plugin-specific settings, capabilities, Caddy routes, site
manifests, listeners или `includes`.

Все settings конкретного plugin создаются и изменяются через Management API,
проверяются по Manifest и settings schema плагина через Plugin SDK и сохраняются Core в SQLite. Core
выдаёт их plugin по versioned REST config pull после `Reload`. Внешняя форма
bootstrap и допустимые поля нормативно заданы в
[Core JSON Schema](/spec/core.schema.json); эта страница её не
дублирует.

Пример намеренно показывает только структуру bootstrap, без реальных ключей,
сертификатов или каталогных credentials:

```yaml
state:
  path: ./state/core.sqlite
management:
  listen: 127.0.0.1:9443
  tls:
    certificate: file:./secrets/management.crt
    key: file:./secrets/management.key
```

Сервисы запускаются оператором вручную. Core не имеет plugin deployment mode,
binary catalog или container provider настройки. Caddy runtime настраивается
как JSON settings Server plugin, не Caddyfile и не отдельным YAML DSL. Схемы и
API управляются из
[Management API](../api/) и [целевой архитектуры](../architecture/target).
