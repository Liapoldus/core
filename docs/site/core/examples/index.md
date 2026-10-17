# Примеры Core

Примеры показывают целевую конфигурацию Core v2. Traffic configuration
задаётся versioned JSON settings Server service, хранится Core в SQLite и
применяется через REST `Reload` и точный service config pull.

- [Service configuration](../api/config) — общий CAS/settings apply lifecycle.
- [Service deployment](../architecture/service-deployment) — регистрация и
  границы ответственности при размещении.
- [Транспорты](../configuration/transports) — HTTP/TLS/WebSocket/SSE;
  публичный L4 вынесен в v3.
- [Целевая архитектура](../architecture/target) — ownership, persistence и
  security invariants.

Конкретная JSON traffic schema публикуется Server service-ом и не дублируется в
Core docs.
