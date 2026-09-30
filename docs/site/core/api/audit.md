# Audit и operations

Audit и durable operations хранятся в SQLite, а не в JSONL-файлах. Контракт
полей и API pagination задан в [OpenAPI](/spec/management.openapi.yaml).

> **Статус:** описанная ниже поверхность — нормативная цель Core v1; она не
> является утверждением, что вся реализация уже готова. Проверенные gates и
> открытые работы перечислены в [roadmap Core v1](../architecture/v1-migration-roadmap#порядок-реализации).

Core audit фиксирует actor service-key/Controller-binding ID, action,
resource type/ID, result, timestamp, request ID и применимые digests. Он не
сохраняет конфигурационные документы целиком, package/artifact contents,
Authorization, secrets, private keys, cookies, plugin payloads или grant
handles. Для конфигурационных mutations фиксируются operation ID, revision,
digest и результат без тела.

Операции REST `Reload`, config pull и plugin-owned
Admin Surface actions сохраняют state, idempotency fingerprint, timestamps и
safe result/problem. После restart операция восстанавливается или явно
помечается failed/recovery-required; она не теряется в памяти процесса.

Для web-операций Core видит только authenticated Constructor binding и его
service key; Core не принимает actor headers как источник identity или
authorization. Constructor audit связывает тот же request/operation ID с
end-user, role, environment и target Core. Именно Constructor audit является
источником персональной identity и решения о доступе.

## Выборка и пагинация

`GET /api/audit` возвращает записи от новых к старым. Порядок детерминирован
монотонной append sequence, назначаемой при добавлении записи; одинаковые или
близкие timestamps не меняют порядок. `cursor` — непрозрачное значение,
возвращённое Core в `nextCursor`: клиент передаёт его без разбора и
изменения, чтобы продолжить ту же выборку. `limit` по умолчанию равен 50,
максимум — 100; допустимы значения от 1 до 100. `nextCursor: null` означает,
что следующей страницы нет.

Срок хранения audit — 90 дней. Retention применяется к видимому набору:
записи старше срока удаляются политикой очистки, и API не обещает их выдачу.
API не предоставляет изменение или удаление отдельных записей: добавление
доступно только как append, а удаление старых записей выполняется retention
policy. Пагинация не фиксирует исторический snapshot через весь срок хранения;
если retention удалил записи между запросами страниц, они больше не входят в
доступную выборку.

Audit record содержит `requestId`, а также `beforeDigest` и `afterDigest`,
когда соответствующее состояние существует; digest поля могут быть `null`,
если они неприменимы. Запросные/ответные payloads, секреты и иные чувствительные
данные не становятся частью audit. Точные поля ответа заданы OpenAPI.

Полный storage/ER контракт:
[Control plane](../architecture/control-plane#sqlite-и-файлы).
