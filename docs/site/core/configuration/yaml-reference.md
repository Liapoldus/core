# Bootstrap `core.yaml`

`core.yaml` содержит bootstrap самого Core: путь к SQLite, Management
listener/TLS, Plugin SDK control listener/trust roots и объявленную оператором
топологию plugin instances. Для каждой replica здесь фиксируются endpoint и
ожидаемая mTLS identity. Management API не создаёт и не меняет эти записи.
Plugin settings хранятся в SQLite. Binary releases и provider connections в v1
Core не управляет.
Файл не задаёт plugin-specific settings, capabilities, Caddy routes, site
manifests, listeners или `includes`.

Все settings конкретного plugin создаются и изменяются через Management API,
проверяются по Manifest и settings schema плагина и сохраняются Core в SQLite.
Core выдаёт их plugin по versioned REST config pull после `Reload`. Внешняя форма
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
pluginControl:
  listen: 127.0.0.1:9444
  publicURL: https://core.internal:9444
  tls:
    certificate: file:./secrets/plugin-control.crt
    key: file:./secrets/plugin-control.key
    replicaClientCA: file:./secrets/plugin-client-ca.crt
    replicaServerCA: file:./secrets/plugin-server-ca.crt
plugins:
  - instanceId: forms-db
    replicas:
      - replicaId: forms-db-1
        endpoint: https://127.0.0.1:9543
        expectedPeerIdentity:
          commonName: forms-db-1
```

Пример показывает структуру, а не готовые credentials или production bind
addresses. Identity и trust material должны соответствовать сертификатам,
выданным оператором. Сервисы запускаются оператором вручную. Core не имеет
plugin deployment mode, binary catalog или container provider настройки.
Server runtime настраивается
как JSON settings Server plugin, не Caddyfile и не отдельным YAML DSL. Схемы и
API управляются из
[Management API](../api/) и [целевой архитектуры](../architecture/target).
