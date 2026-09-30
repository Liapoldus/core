# Bootstrap contract

Подробная reference-страница переехала в
[справочник `core.yaml`](yaml-reference). Bootstrap задаёт только то, что
нужно самому единственному Core для старта: SQLite path, локальный package
store, Management TLS и параметры trusted catalog. Deployment mode задаётся
для каждого plugin instance отдельно и не выбирается глобальным флагом Core.

Plugin settings и per-replica endpoints
редактируются через Management API и сохраняются в SQLite. Они не находятся в
`core.yaml`, `site.yaml`, Caddyfile или YAML includes. Plugin-to-plugin
authorization policies в v1 принадлежат вызывающим plugins, а не Core. Caddy
traffic JSON является Server-plugin-owned settings document. Machine-readable
схема — [core.schema.json](/spec/core.schema.json).

Изменение bootstrap применяется контролируемым restart Core; изменения plugin
config проходят durable REST Reload/config-pull operation без ручного редактирования
файлов. См. [REST lifecycle](../architecture/control-plane) и
[Security](security).
