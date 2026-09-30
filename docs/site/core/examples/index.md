# Примеры Core

Примеры показывают целевую конфигурацию Core v1. Traffic configuration
задаётся versioned JSON settings Server plugin, хранится Core в SQLite и
применяется через REST `Reload` и точный plugin config pull.

- [Plugin configuration](../api/config) — общий CAS/settings apply lifecycle.
- [Plugin deployment](../architecture/plugin-deployment) — ручной запуск v1 и
  отложенные deployment modes v2.
- [Транспорты](../configuration/transports) — HTTP/TLS/WebSocket/SSE в v1;
  публичный L4 вынесен в v2.
- [Целевая архитектура](../architecture/target) — ownership, persistence и
  security invariants.

Конкретная JSON traffic schema публикуется Server plugin-ом и не дублируется в
Core docs.
