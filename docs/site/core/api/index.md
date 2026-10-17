# Core Management API

Management API управляет Core desired-state: service instance metadata и
settings, access, operations и audit. Membership service replicas определяется
аутентифицированной регистрацией и активными leases.
Standalone CLI доставляет project revisions через [config bundle API](config-bundles);
Core не содержит пользовательского CLI и не читает проектные файлы.
API не управляет service-to-service interaction policies. Оно не
устанавливает, не запускает, не останавливает, не перезапускает, не
масштабирует и не удаляет service processes/containers. Оно не проксирует public
traffic, не предоставляет Caddy Admin API и не содержит Caddy-specific route,
group или TLS endpoints.

## Поверхность API

| Область | Назначение |
| --- | --- |
| Core settings | Read/update собственных настроек Core с revision CAS; desired/effective state явно разделены. Это не конфигурация сервисов. |
| Services | Instance/replica observations, Manifest/schema, raw JSON settings, rollback и общий REST Reload/config-pull lifecycle. |
| Admin Surface | Общая авторизованная граница для service-owned management actions и UI descriptors. |
| Operations | Polling durable operations и generation/replica readiness. |
| Access/Audit | Выпуск и чтение metadata service key, operator actor и append-only audit. |

Все формы API подчиняются одной authorization policy; точные схемы и status/error catalog — в
[OpenAPI](../../spec/management.openapi.yaml) и
[errors contract](../../spec/errors.json).

Подробно: [аутентификация](authentication),
[service configuration](config) и [operations](operations).
Management API также предоставляет scoped catalog для
[service secret references](../configuration/secrets).

Core-owned settings доступны только operator Management API через
`GET/PUT /api/v2/settings`. `If-Match` защищает запись от stale revision;
`desiredRevision`, `effectiveRevision`, `pending` и `restartRequired` позволяют
отличить сохранённые настройки от уже активных. Изменение само по себе не
перезапускает Core. CLI/Studio должны скрывать host/container paths и передавать
в renderer только безопасные target-scoped references. Полная форма запроса и
ответа закреплена в [Management OpenAPI](../../spec/management.openapi.yaml).

## Core-backed runtime metadata

Для local runtime CLI объявляет Core только безопасные metadata живой replica:

```text
POST /api/services/{instanceId}/replicas/register
GET  /api/services/{instanceId}/manifest
GET  /api/services/{instanceId}/schema
GET  /api/services/{instanceId}/status
```

После registration Core через Service SDK REST client сам получает manifest,
configuration schema, identity и readiness. До подтверждения Core сопоставляет
заявленные CLI artifact ID/version/SHA-256 и control endpoint с аутентифицированной
replica lease и SDK release/REST claims; если сервис объявляет peer listener,
его endpoint тоже должен совпасть. Затем Core проверяет service ID/version в
live manifest. SDK release digest остаётся аутентифицированным claim процесса,
а не независимым доказательством происхождения бинарника. Status включает
`artifactId`, `artifactVersion`, `artifactDigest` и `replicaId` из той же
аутентифицированной live lease, а также список release claims всех активных
replica. Metadata endpoint закрывается ошибкой, если активные replica сообщают
разные version/digest; Studio также сверяет неизменность состава replica между
чтениями до и после получения manifest/schema. Это fail-closed барьер для
mixed-release rollout, а не независимое доказательство байтов бинарника.
Для каждой активной replica status дополнительно возвращает Core-owned
`configuration` с `desiredGeneration`, `appliedGeneration` и состоянием
`unconfigured`, `unobserved`, `pending`, `acknowledged`, `stale`, `failed` или
`unreachable`. Durable ACK связан с точным `incarnationId`; наблюдение прежнего
процесса с тем же `replicaId` не подтверждает новый процесс. Это позволяет
отображать partial rollout, не подменяя его общей readiness.
CLI/Studio могут сверить текущую metadata с выбранной Project artifact reference.
CLI не вызывает service REST и не
получает SDK client. Core применяет configuration generations и публикует peer
directory, а data plane остаётся прямым:
`Server → wool → Echo`.
