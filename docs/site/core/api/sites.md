# Site publishing

Core v1 не предоставляет `/api/sites` и не владеет Caddy group release API.
Публикация site artifacts — функция Server plugin и доступна через его
объявленную Admin Surface. Server plugin хранит immutable releases и
`current/previous` на persistent filesystem; конфигурация самой traffic-схемы
остаётся в Core SQLite. Core уведомляет Server plugin REST `Reload`, а тот
запрашивает точный settings generation у Core.

Общая Management API граница применяет авторизацию, plugin scope, лимиты,
идемпотентность и audit; Server plugin проверяет архив и атомарно активирует
site release. Используется generic plugin Admin Action: JSON metadata и один
бинарный `.tar.gz` передаются одним `multipart/form-data` запросом; Core
потоково передаёт архив plugin через общий REST artifact endpoint Plugin SDK и
не буферизует его целиком. Caddy-specific endpoint в Core не появляется.

V1 upload limit: artifact не более 128 MiB; JSON metadata — 64 KiB; multipart
boundaries/part headers — ещё 64 KiB. Полный request body не превышает 128 MiB
плюс 128 KiB, считая фактически принятые bytes, в том числе при chunked
transfer. Распакованный aggregate ограничен 512 MiB, archive содержит не более
10 000 файлов, а aggregate ratio `expandedBytes / compressedArtifactBytes` —
не более 100:1; пустой/нулевой compressed artifact отклоняется. Превышение
лимита, неверный digest, повреждённый gzip/tar, traversal, symlink/hardlink,
duplicate или case/NFC collision либо некорректный site manifest отвергают весь candidate. До
успешной полной проверки `current` и `previous` не меняются. Plugin публикует
release как неизменяемую revision и атомарно переключает current pointer;
предыдущая revision становится `previous`.

Архив обязан содержать ровно один regular-file `site-manifest.json` в корне.
Manifest v1 содержит `schemaVersion: 1`, `siteId`, `documentRoot` и
`indexDocument`; `siteId` должен в точности совпасть с `payload.siteId` без
регистрового или Unicode-нормализующего преобразования. `documentRoot` — POSIX
relative directory внутри архива (`.` означает корень); `indexDocument` — POSIX
relative regular-file path внутри этого корня. Запросы к `/` и directory URL
добавляют `indexDocument`; SPA fallback нет, отсутствующий путь даёт 404.
Traversal, absolute/backslash paths, отсутствующий или дублирующийся manifest,
неподдерживаемая версия, неверный site ID или отсутствующий index отклоняют весь
candidate; active pointers остаются неизменными. Служебный root manifest никогда
не выдаётся как статический файл. Подробный owner contract находится в локальном
Server plugin worktree (`contracts/v1/site-publish-manifest.json`) и пока не
опубликован.

Публикация асинхронна: API отвечает `202 Accepted` с durable operation ID.
Пока архив проверяется и candidate готовится, `current` не меняется; только
успешный terminal state operation активирует release. При ошибке/перезапуске
сохраняется прежний current, а operation позволяет оператору получить итог без
повторной загрузки. Точный повтор с тем же `Idempotency-Key` возвращает ту же
operation; ключ, привязанный к другому metadata/artifact digest, конфликтует.

См. [Caddy ownership](../architecture/target) и
[plugin Admin UI](../../plugins/admin-pages).
