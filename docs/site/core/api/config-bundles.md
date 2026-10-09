# Config bundles

Standalone `liapoldus` CLI передаёт конфигурацию в Core только через
Management API. Core не читает Git checkout, не принимает путь к файлам и не
открывает внешний SQLite.

## Plan

`POST /api/config-bundles/plan` требует Bearer-аутентификацию и JSON envelope:

```json
{
  "project": {
    "id": "forms-platform",
    "repository": "https://github.com/example/forms-platform.git",
    "revision": "0123456789abcdef"
  },
  "bundle": {
    "schemaVersion": "core-config/v2",
    "digest": "sha256:..."
  },
  "target": { "environment": "production" },
  "services": [
    { "id": "forms", "settings": { "opaque": true } }
  ],
  "links": []
}
```

Plan проверяет envelope, JSON settings и наличие service instances. Он не
создаёт desired state и не запускает rollout. В ответе для каждого service
возвращаются `currentRevision`, `currentDigest` и `schemaVersion`.

## Apply

`POST /api/config-bundles/apply` использует тот же envelope и обязательный
`Idempotency-Key`. Core создаёт service-scoped operations через существующий
configuration service и возвращает:

```json
{
  "operations": ["operation-id"],
  "requestId": "request-id"
}
```

CLI обязан сохранить связь между commit, bundle digest и operation IDs и
получать итоговое состояние через `GET /api/operations/{id}`. Состояния
`failed` и `degraded` не считаются успешным применением.

Settings остаются opaque для generic Core envelope. Их схема и product-specific
семантика принадлежат зарегистрированному service. Core сам владеет SQLite,
generations, replicas, leases, rollout и audit; CLI не записывает эти таблицы.
