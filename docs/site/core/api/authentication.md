# Аутентификация и доступ Management API

Management API — отдельная control-plane поверхность Core. Все операции
авторизуются на сервере; браузер не получает Core service credential. До
готовности Core v1 Core имеет одну системную роль `platform-admin` и не
дублирует user/RBAC model Constructor.

## Web Controller

Backend Controller устанавливает private HTTPS connection к Management API с
mTLS и отдельным Bearer service credential для каждой Core binding. В
каждом запросе Core проверяет Bearer authorization и клиентскую TLS
identity на TLS handshake. Core credential имеет одну роль
`platform-admin`; он авторизует binding, но не передаёт Core пользовательские
permissions Constructor. Constructor проверяет end-user, role и environment до
вызова. Browser не получает Core token. Core audit фиксирует authenticated
binding/key и общий request/operation ID; персональная идентичность и решение
Constructor хранятся в его собственном audit и связываются тем же operation ID.
Core не принимает actor headers как authority и не использует их для
authorization. Token хранится backend-ом в защищённом secret store.

Отсутствующий или недоверенный клиентский certificate завершается отказом TLS
handshake до HTTP; в этом случае нельзя вернуть Management Problem/HTTP `401`.
После успешного TLS handshake отсутствующий, истёкший или отозванный Bearer key
возвращает `401 management_bearer_required`. Недостаточная authenticated role
возвращает `403 forbidden`.

## Desktop

Desktop использует ограниченный SSH port-forward через внешний OpenSSH/bastion
к loopback Management listener. SSH policy разрешает только port forwarding и
запрещает shell, SFTP и agent forwarding. Go bridge проверяет TLS identity
удалённого Core и передаёт краткоживущий Bearer credential из OS credential
store. Отсутствие пользовательского login разрешено только для single-user
desktop; удалённый Core всё равно требует полноценную service
authorization.

## Bootstrap и audit

Первый `platform-admin` credential создаётся локальным bootstrap command и
показывается ровно один раз. SQLite хранит только verifier и metadata.
Management API v1 поддерживает выпуск и чтение metadata credentials; отдельные
API rotation/revocation в v1 контракт не входят. Срок действия проверяется на
каждом запросе. Каждая успешная и неуспешная mutation
записывает actor, binding, action, resource, result и request ID в audit; raw
credentials и TLS material туда не попадают.

Management TLS roots отделены от plugin workload roots и Caddy ACME state.
Для web подключения используется private network/VPN плюс mTLS и Bearer; для
desktop tunnel удалённого Core — аналогичный trust boundary без требования
настраивать mTLS непосредственно в desktop app.

## Авторизация по операциям

За пределами unauthenticated `/healthz` все `/api/**` endpoints требуют
валидный Bearer key с ролью `platform-admin`; закрытые Management deployments
дополнительно применяют TLS transport rules выше. Более мелкие user/role/
environment permissions принадлежат Controller/Constructor, а не Core.

| Операция | Дополнительное правило |
| --- | --- |
| Чтение status, plugin metadata/settings, operations и audit | Нужен `platform-admin`; каждый запрос к `operationId` повторно авторизуется. |
| Изменение settings и rollback | Нужны `Idempotency-Key` и `If-Match` согласно OpenAPI; операции проверяются deny-by-default. |
| Plugin process/workload lifecycle | В v1 таких Management API операций нет. Оператор вручную запускает и обслуживает plugin processes; Core управляет только конфигурацией, объявленными endpoint/identity, scoped secret grants, health/readiness и audit. |
| Plugin Admin Surface query/action | Нужны instance scope, active surface digest (`If-Match`), schema-valid metadata и action-specific limits. |
| Выпуск service key | Только bootstrap/admin authority; raw token возвращается только в ответе выдачи и не доступен через list/read/audit. Rotation/revocation API отложены до v2. |

Общие problem mappings: TLS client-certificate failure не является HTTP
response; Bearer failure — `401`; authenticated authorization denial — `403`;
resource/idempotency conflict — `409`; stale `If-Match` — `412`; invalid
schema — `422`; byte limit —
`413`; unavailable dependency/recovery — `503`. Точная пара `status/code`
определена в [error catalog](/spec/errors.json); endpoint-specific success
statuses и обязательные headers — в [OpenAPI](/spec/management.openapi.yaml).

Полная структура bootstrap и plugin credential границ находится в
[Security configuration](../configuration/security) и
[целевой архитектуре](../architecture/target).
