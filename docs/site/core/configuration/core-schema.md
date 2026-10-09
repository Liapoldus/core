# Схема bootstrap `core.yaml`

Нормативная минимальная схема опубликована в
[core.schema.json](/spec/core.schema.json). Файл содержит только пути
локального состояния Core и Management listener/TLS. Plugin endpoints и
ожидаемые replica identities регистрируются через Management API после того,
как оператор выбранными средствами запустил сервисы. Core bootstrap ни в
одной версии не содержит deployment-mode selector или provider
connection.

Настройки plugins, фиксированные endpoints, взаимодействия и Caddy traffic
JSON хранятся Core в SQLite и запрашиваются плагинами через REST после `Reload`. Они не
задаются в `core.yaml`, Caddyfile, `site.yaml` или YAML includes. Подробности
см. в [Bootstrap contract](bootstrap) и [целевой архитектуре](../architecture/target).
