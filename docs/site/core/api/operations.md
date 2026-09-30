# Durable operations

Mutation, требующая внешнего plugin/process effect, возвращает durable
`operationId`. Клиент опрашивает
`GET /api/operations/{operationId}`; операция не является proxy к public
traffic или Caddy Admin API. Точные JSON-типы — в
[Management OpenAPI](/spec/management.openapi.yaml), коды ошибок — в
[error catalog](/spec/errors.json).

## Состояния

```text
pending → running → succeeded
                  ↘ failed
                  ↘ degraded
```

| Состояние | Значение |
| --- | --- |
| `pending` | Запись операции и ключ идемпотентности сохранены; внешнее действие ещё не начато. |
| `running` | Внешний процесс/plugin выполняет действие или ожидается ACK. |
| `succeeded` | Требуемый effect подтверждён и durable pointer/state committed. |
| `failed` | Действие завершилось ошибкой; active state не меняется. |
| `degraded` | Effect мог частично примениться, компенсация не подтверждена либо recovery требует reconcile; затронутая capability fenced. |

`succeeded`, `failed` и `degraded` — terminal для данной operation. Для
исправления создаётся новая mutation/operation с актуальным `If-Match`; отдельного
endpoint отмены в v1 нет. Отмена HTTP запроса до принятия durable operation не
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
`previous` и `staging` в `active` до Reload. Plugin сохраняет прежнюю in-memory
конфигурацию, если candidate не применён; Core придерживается
roll-forward и fencing: подтверждённые остаются на target, остальные повторно
получают Reload, operation остаётся `degraded` до полного согласования; traffic
получают только replicas с требуемым generation.

Artifact Admin Action сначала проходит auth, `If-Match`, metadata schema,
content-type и byte-limit проверки. Core резервирует operation ID, затем
потоково передаёт artifact plugin. `202 Accepted` возвращается только после
подтверждения plugin, что operation и входной artifact приняты устойчиво.
Application effects, archive staging и опубликованные файлы принадлежат plugin;
Core не хранит artifact bytes в SQLite или на диске. Site `current`/`previous`
меняются только после успешной валидации и terminal success plugin operation.
После `202` клиент отсоединён от plugin Stream; polling обращается только к
Core, который сохраняет безопасный status/error code. Подробный generic
artifact lifecycle определяется Plugin SDK REST contract; не добавляйте
capability-specific management endpoint в Core.

## Идемпотентность и доступ

`Idempotency-Key` уникален в области authenticated actor + resource/action.
Точный повтор того же ключа и input fingerprint возвращает существующую
operation; повтор ключа с иным fingerprint даёт `409 idempotency_conflict`.
Для multipart fingerprint строится по canonical metadata, стабильному scope
(`instance/page/action/surface digest`) и SHA-256 фактически принятого artifact;
переданный клиентом размер или digest не заменяет подсчёт/хеширование Core.
Границы multipart не включаются в fingerprint.

Operation ID непрозрачен и не является правом доступа: каждый GET повторно
авторизуется. Запись содержит только безопасные kind/resource IDs, state,
timestamps, digest и error code. Она не возвращает raw request/response,
artifact metadata с секретами, credentials, plugin text, cookie values или
secret references. Error details проходят тот же redaction, что и Management
API. Audit intent/result связываются с operation ID и digest, но не с body.

Общие durable semantics и crash points описаны в
[control plane](../architecture/control-plane); multipart site upload — в
[site publishing](sites); Admin Surface artifact contract — в
[Plugin Admin Pages](/plugins/admin-pages).
