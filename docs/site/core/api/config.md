# Plugin configuration API

Core хранит desired settings каждого instance в SQLite как точные UTF-8 bytes
исходного JSON-документа. Нормативная schema и её версия принадлежат
репозиторию соответствующего plugin; Plugin SDK предоставляет общий REST
endpoint для обнаружения schema. Core проверяет размер, UTF-8, JSON syntax,
повторяющиеся ключи и документ по plugin-owned JSON Schema, не декодируя его в
product-модель и не сериализуя повторно. SHA-256 считается по тем же байтам,
которые были приняты и сохранены.

`PUT /api/plugins/{pluginId}/settings` принимает сам JSON object плагина в теле
запроса — без `{ "config": ... }` обёртки. CAS и идемпотентность передаются
заголовками `If-Match` и `Idempotency-Key`. Синтаксически или schema-invalid
документ не меняет поколения и не вызывает `Reload`.

После записи Core вызывает `POST /_liapoldus/v1/reload` без конфигурационного
payload; plugin сам запрашивает точную generation через
`GET /internal/v1/plugin-config/{generation}`. Оба пути принадлежат
versioned Plugin SDK contract; config pull требует per-replica mTLS.

Изменение выполняется с revision/CAS (`If-Match`). Повтор idempotency key
возвращает существующую operation; конфликт версии не меняет active generation.
Каждый instance хранит до трёх durable поколений в слотах `active`, `previous`
и `staging`. После validation исходные candidate bytes сохраняются в
непубликуемый `staging` вместе с durable operation. Promotion одной
SQLite-транзакцией удаляет прежний `previous`, переносит бывший `active` в
`previous` и `staging` в `active`, затем публикует snapshot до Reload fan-out.
Operation остаётся pending/degraded, пока
обязательные replicas не подтвердили новое active поколение; повторная mutation
на время незавершённого rollout отклоняется.
Частичный успех обрабатывается roll-forward: подтверждённые replicas обслуживают
target, неподтвердившие fenced/degraded и повторно получают Reload. Rollback
доступен через `POST /api/plugins/{pluginId}/rollback`: Core меняет `active` и
`previous` до pull/ACK и уведомляет плагин обычным `Reload`.

Caddy использует тот же generic plugin-settings flow. Его schema описывает
traffic configuration в JSON; Server plugin компилирует её во внутреннюю
Caddy runtime-конфигурацию. Core не принимает Caddyfile/group release и не
публикует native Admin API.

Точный route, CAS header, request/response shapes и ошибки задаёт
[Management OpenAPI](../../spec/management.openapi.yaml). Plugin-side settings
schemas принадлежат соответствующим plugin contract. Общие REST lifecycle
types и endpoints принадлежат отдельному Plugin SDK contract.
`pluginprotocol` используется только для прямых plugin-to-plugin вызовов и не
содержит REST config/lifecycle контрактов.
