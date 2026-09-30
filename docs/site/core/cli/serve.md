# `core serve`

`core serve` запускает только Core Management listener, восстанавливает
SQLite desired state и подключается к заранее вручную запущенным plugin
endpoints. Core не запускает и не supervises plugin binaries/containers.

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

Bootstrap fields описаны в [core.yaml reference](../configuration/yaml-reference),
а полная lifecycle model — в
[target architecture](../architecture/target) и
[plugin deployment](../architecture/plugin-deployment).
