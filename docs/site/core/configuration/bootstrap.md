# Bootstrap contract

Подробная reference-страница переехала в
[справочник `core.yaml`](yaml-reference). Bootstrap задаёт SQLite path,
Management TLS, Plugin SDK control listener/trust roots и объявленную
оператором статическую топологию plugin instances.

Plugin settings редактируются через Management API и сохраняются в SQLite.
Фиксированные per-replica endpoints и ожидаемые identities объявляются только
в `core.yaml`; Management API не регистрирует и не меняет topology. Они не
находятся в `site.yaml`, Caddyfile или YAML includes. Plugin-to-plugin
authorization policies в v1 принадлежат вызывающим plugins, а не Core. Caddy
traffic JSON является Server-plugin-owned settings document. Machine-readable
схема — [core.schema.json](/spec/core.schema.json).

Изменение bootstrap применяется контролируемым restart Core; изменения plugin
config проходят durable REST Reload/config-pull operation без ручного редактирования
файлов. См. [REST lifecycle](../architecture/control-plane) и
[Security](security).
