# Аутентификация и доступ Management API

Management API — отдельная control-plane поверхность Core. Все операции
авторизуются на сервере; browser-клиент не получает Core service credential.
В Core v2 есть одна системная роль `platform-admin`; персональные учётные
записи и RBAC-модель не входят в Core.

## Сетевые клиенты

Доверенный web backend подключается к Management API по private HTTPS с mTLS и
отдельным Bearer service credential для каждого Core binding. Core проверяет
Bearer authorization и клиентскую TLS identity на TLS handshake. Credential
имеет роль `platform-admin` и авторизует binding. Владелец web-клиента отвечает
за аутентификацию пользователей и собственную персональную аудит-запись;
browser не получает Core token. Core не принимает actor headers как authority.

Отсутствующий или недоверенный клиентский certificate завершает TLS handshake
до HTTP; Management Problem/HTTP `401` в этом случае не возвращается. После
успешного handshake отсутствующий, истёкший или отозванный Bearer key даёт
`401 management_bearer_required`; недостаточная роль — `403 forbidden`.

## Локальное и удалённое управление

Локальный оператор может использовать CLI и loopback Management listener.
Удалённый операторский клиент может пройти через ограниченный SSH port-forward
к loopback API; tunnel policy запрещает shell, SFTP и agent forwarding. TLS
identity Core проверяется, Bearer credential хранится в защищённом хранилище
операционной системы или backend-а.

Первый `platform-admin` credential создаётся локальной bootstrap-командой и
показывается ровно один раз. SQLite хранит только verifier и metadata.
Management API поддерживает выпуск и чтение metadata credentials; отдельные
API rotation/revocation не входят в текущий scope. Срок действия проверяется при каждом
запросе. Mutation записывает actor key, action, resource, result и request ID
в audit; raw credentials и TLS material туда не попадают.

Management TLS roots отделены от plugin workload roots и Server ACME state.
Для удалённого Management API применяются private network/VPN, mTLS и Bearer.

## Авторизация по операциям

За исключением unauthenticated `/healthz`, все `/api/**` endpoints требуют
валидный Bearer key с ролью `platform-admin`. Более мелкие пользовательские
роли и permissions в Core v2 не предоставляются.

| Операция | Дополнительное правило |
| --- | --- |
| Чтение status, plugin metadata/settings, operations и audit | Нужен `platform-admin`; каждый запрос к `operationId` повторно авторизуется. |
| Изменение settings и rollback | Нужны `Idempotency-Key` и `If-Match` согласно OpenAPI; операции проверяются deny-by-default. |
| Plugin process/workload lifecycle | Таких Management API операций нет. Оператор вручную запускает и обслуживает plugin processes. |
| Plugin Admin Surface query/action | Нужны `platform-admin`, instance scope, active surface digest, schema-valid metadata и action-specific limits. |
| Выпуск service key | Только bootstrap/admin authority; raw token возвращается только при выдаче и не доступен через list/read/audit. |

Общие problem mappings: TLS client-certificate failure не является HTTP
response; Bearer failure — `401`; authorization denial — `403`;
resource/idempotency conflict — `409`; stale `If-Match` — `412`; invalid
schema — `422`; byte limit — `413`; unavailable dependency/recovery — `503`.
Точная пара `status/code` задана в [error catalog](/spec/errors.json), а
success statuses и обязательные headers — в [OpenAPI](/spec/management.openapi.yaml).

Полная структура bootstrap и plugin credential границ находится в
[Security configuration](../configuration/security) и
[целевой архитектуре](../architecture/target).
