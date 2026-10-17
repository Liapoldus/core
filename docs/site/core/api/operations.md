# Durable operations

Mutation, требующая внешнего service/process effect, возвращает durable
`operationId`. Клиент опрашивает
`GET /api/operations/{operationId}`; операция не является proxy к public
traffic или Caddy Admin API. Точные JSON-типы — в
[Management OpenAPI](/spec/management.openapi.yaml), коды ошибок — в
[error catalog](/spec/errors.json).

После потери ответа на mutation доверенный Management-клиент может вызвать
`POST /api/operations/resolve` с `scope` и ограниченным списком исходных
idempotency keys. Core ищет только операции текущего authenticated actor и
возвращает найденные `id`, `state`, `resourceId` и безопасный `errorCode`; ключи,
request body и конфигурация не возвращаются. Метод предназначен для восстановления
caller journal и не заменяет обычный polling по operation ID.

## Состояния

```text
pending → running → succeeded
                  ↘ failed
                  ↘ degraded
```

| Состояние | Значение |
| --- | --- |
| `pending` | Запись операции и ключ идемпотентности сохранены; внешнее действие ещё не начато. |
| `running` | Внешний процесс/service выполняет действие или ожидается ACK. |
| `succeeded` | Требуемый effect подтверждён и durable pointer/state committed. |
| `failed` | Попытка завершилась ошибкой. Если ошибка возникла до promotion, `active` не меняется; после promotion новое поколение остаётся желаемым `active`, а не подтвердившие его replicas остаются fenced/degraded до reconcile или явного rollback. |
| `degraded` | Effect мог частично примениться, компенсация не подтверждена либо recovery требует reconcile; затронутая capability fenced. |

`succeeded`, `failed` и `degraded` — terminal для данной operation. Для
исправления создаётся новая mutation/operation с актуальным `If-Match`; отдельного
endpoint отмены нет. Отмена HTTP запроса до принятия durable operation не
означает отмену уже начатого внешнего effect.

Каждая mutation сначала сохраняет Core operation reservation и scope ключа
идемпотентности в SQLite, затем выполняет внешний effect. Успех фиксируется
только после обязательных per-replica ACK и SQLite commit. При crash Core
восстанавливает pending/running operation по journal; если подтверждение или
compensation нельзя установить, переводит её в `degraded`, не сообщает ложный
success и не открывает соответствующую capability как Ready.

Для settings mutation Core сначала валидирует candidate,
затем сохраняет точные bytes в durable, но непубликуемый `staging` slot вместе
с operation. Promotion удаляет старый `previous`, перемещает `active` в
`previous` и `staging` в `active` до Reload. Service сохраняет прежнюю in-memory
конфигурацию, если candidate не применён; Core придерживается
roll-forward и fencing: подтверждённые остаются на target, остальные повторно
получают Reload, operation остаётся `degraded` до полного согласования; traffic
получают только replicas с требуемым generation.

Artifact Admin Action сначала проходит auth, metadata schema, content-type и
byte-limit проверки. `If-Match` передаётся сервису, если задан, но может быть
опущен для создания первой revision: artifact metadata и service-owned CAS
защищают публикацию от потери обновлений. Для последующих ревизий service
сверяет `expectedCurrentRevision` с текущим состоянием. Core резервирует operation ID, затем
потоково передаёт artifact service. `202 Accepted` возвращается только после
подтверждения service, что operation и входной artifact приняты устойчиво.
Application effects, archive staging и опубликованные файлы принадлежат service;
Core не хранит artifact bytes в SQLite или на диске. Site `current`/`previous`
меняются только после успешной валидации и terminal success service operation.
После `202` клиент отсоединён от service Stream; polling обращается только к
Core, который сохраняет безопасный status/error code. Подробный generic
artifact lifecycle определяется Service SDK REST contract; не добавляйте
capability-specific management endpoint в Core.

## Идемпотентность и доступ

`Idempotency-Key` уникален в области authenticated actor + resource/action.
Точный повтор того же ключа и input fingerprint возвращает существующую
operation; повтор ключа с иным fingerprint даёт `409 idempotency_conflict`.
Для multipart fingerprint строится по canonical metadata, стабильному scope
(`instance/page/action/surface digest`) и SHA-256 фактически принятого artifact;
переданный клиентом размер или digest не заменяет подсчёт/хеширование Core.
Границы multipart не включаются в fingerprint.

Это правило относится к durable mutations и multipart artifact operations,
которые создают `operationId`. Синхронный JSON-вызов service Admin Action не
является durable operation: `Idempotency-Key` для него — только корреляционное
значение для запроса и audit. Каждый отдельно принятый HTTP-запрос заново
вызывает action, даже если actor, scope, ключ и payload совпадают; Core создаёт
audit событие на каждое выполнение и не возвращает сохранённый ответ. Поэтому
повторный `forms.delete` после успешного первого вызова выполняется снова и
возвращает service-ошибку `not_found`, а не replay первого успешного ответа.
Это не exactly-once механизм: после сетевой ошибки клиент сам решает, повторять
ли запрос, и повтор может повторно выполнить внешние эффекты. Автоматический
replay неизвестного результата не выполняется. Artifact Admin Actions, для
которых создаётся durable operation, сохраняют operation-level deduplication,
описанную выше.

Operation ID непрозрачен и не является правом доступа: каждый GET повторно
авторизуется. Запись содержит только безопасные kind/resource IDs, state,
timestamps, digest и error code. Она не возвращает raw request/response,
artifact metadata с секретами, credentials, service text, cookie values или
secret references. Error details проходят тот же redaction, что и Management
API. Audit intent/result связываются с operation ID и digest, но не с body.

Для configuration rollout `GET /api/operations/{id}` дополнительно возвращает
`replicaOutcomes`: durable result по каждой replica из замороженного rollout
cohort. Поля ограничены `replicaId`, `desiredGeneration`, `state` (`pending`,
`applied` или `failed`) и, при ошибке, безопасным `failureCode`. Endpoint,
incarnation, credentials и конфигурация не раскрываются. Если rollout ещё
согласуется, неподтвердившие replicas остаются `pending`; завершённая ошибкой
операция показывает их как `failed`.

Общие durable semantics и crash points описаны в
[control plane](../architecture/control-plane); multipart site upload — в
[site publishing](sites); Admin Surface artifact contract — в
[Service Admin Pages](/services/admin-pages).
