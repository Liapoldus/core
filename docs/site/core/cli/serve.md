# `core serve`

`core serve` запускает Core Management listener из настроек, активированных в
SQLite. Environment задаёт путь `CORE_SQLITE_PATH`; `CORE_INIT_*` при serve не
читаются. Core не запускает plugin processes.

Startup считается готовым после восстановления SQLite/journal, загрузки
active in-memory snapshot и подключения к зарегистрированным плагинам для
Manifest/schema/identity checks и подтверждения REST configuration
generations. Caddy — отдельный вручную запускаемый plugin process, не часть
Core бинарника. В v1 его instance ровно один; Core не создаёт Caddy runtime и
не принимает public traffic.

Если config candidate не применился, прежняя revision остаётся active. Если
недоступен один plugin, Core остаётся ready в degraded состоянии, а связанные
с ним capabilities сообщают bounded unavailable. Невосстановимая ошибка
SQLite или generation journal блокирует Management readiness.

Bootstrap fields описаны в [руководстве запуска](../configuration/bootstrap),
а полная lifecycle model — в
[target architecture](../architecture/target) и
[plugin deployment](../architecture/plugin-deployment).
