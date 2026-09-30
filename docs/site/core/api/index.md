# Core Management API

Management API управляет Core desired-state: plugin instances и заранее
зарегистрированными plugin endpoints, settings, access, operations и audit.
В v1 API не управляет plugin-to-plugin interaction policies. Оно не
устанавливает, не запускает, не останавливает, не перезапускает, не
масштабирует и не удаляет plugin processes/containers. Оно не проксирует public
traffic, не предоставляет Caddy Admin API и не содержит Caddy-specific route,
group или TLS endpoints.

## Поверхность API

| Область | Назначение |
| --- | --- |
| Plugins | Instance/replica metadata, fixed endpoints, Manifest/schema, raw JSON settings, rollback и общий REST Reload/config-pull lifecycle. |
| Admin Surface | Общая авторизованная граница для plugin-owned management actions и UI descriptors. |
| Operations | Polling durable operations и generation/replica readiness. |
| Access/Audit | Выпуск и чтение metadata service key, operator actor и append-only audit. |

Все формы API подчиняются одной authorization policy; точные схемы и status/error catalog — в
[OpenAPI](../../spec/management.openapi.yaml) и
[errors contract](../../spec/errors.json).

Подробно: [аутентификация](authentication),
[plugin configuration](config) и [operations](operations).
