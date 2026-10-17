# Service configuration API

Core хранит desired settings каждого instance в SQLite как точные UTF-8 bytes
исходного JSON-документа. Нормативная schema и её версия принадлежат
репозиторию соответствующего service; Service SDK предоставляет общий REST
endpoint для обнаружения schema. Core проверяет размер, UTF-8, JSON syntax,
повторяющиеся ключи и документ по service-owned JSON Schema, не декодируя его в
product-модель и не сериализуя повторно. SHA-256 считается по тем же байтам,
которые были приняты и сохранены.

`PUT /api/services/{serviceId}/settings` принимает сам JSON object сервиса в теле
запроса — без `{ "config": ... }` обёртки. CAS и идемпотентность передаются
заголовками `If-Match` и `Idempotency-Key`. Синтаксически или schema-invalid
документ не меняет поколения и не вызывает `Reload`.

После записи Core вызывает `POST /_liapoldus/v1/reload` без конфигурационного
payload; service сам запрашивает точную generation через
`GET /internal/v1/service-config/{generation}`. Оба пути принадлежат
versioned Service SDK contract; config pull требует per-replica mTLS.

Изменение выполняется с revision/CAS (`If-Match`). Повтор idempotency key
возвращает существующую operation; конфликт версии не меняет active generation.
Каждый instance хранит до трёх durable поколений в слотах `active`, `previous`
и `staging`. После validation исходные candidate bytes сохраняются в
непубликуемый `staging` вместе с durable operation. Promotion одной
SQLite-транзакцией удаляет прежний `previous`, переносит бывший `active` в
`previous` и `staging` в `active`, затем публикует snapshot до Reload fan-out.
Core один раз уведомляет каждую обязательную replica. Если все подтвердили новое
active поколение, operation завершается `succeeded`; при отказе или недоступности
replica operation завершается `failed`, но desired `active` не откатывается.
Подтвердившие replicas обслуживают target, неподтвердившие остаются
fenced/degraded. Readiness monitor не повторяет `Reload` в фоне. После устранения
причины оператор вручную перезапускает Core: startup reconciliation сверяет
replicas и повторно уведомляет только отставшие.
Rollback доступен через `POST /api/services/{serviceId}/rollback`: Core меняет
`active` и `previous` до pull/ACK и выполняет тот же однократный fan-out.

Caddy использует тот же generic service-settings flow. Его schema описывает
traffic configuration в JSON; Server service компилирует её во внутреннюю
Caddy runtime-конфигурацию. Core не принимает Caddyfile/group release и не
публикует native Admin API.

Точный route, CAS header, request/response shapes и ошибки задаёт
[Management OpenAPI](../../spec/management.openapi.yaml). Service-side settings
schemas принадлежат соответствующим service contract. Общие REST lifecycle
types и endpoints принадлежат отдельному Service SDK contract.
`wool` используется только для прямых service-to-service вызовов и не
содержит REST config/lifecycle контрактов.
