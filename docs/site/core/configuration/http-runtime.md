# HTTP runtime boundary

HTTP/HTTPS, HTTP/2/3, TLS, reverse proxy, static serving, WebSocket и SSE
исполняет отдельный Server service на базе Caddy. Core не встраивает Caddy и не
обслуживает пользовательский traffic.

Единственный source of truth настроек — versioned JSON Server service в SQLite
Core. Это строгая Liapoldus schema, принадлежащая Server service; произвольный
raw Caddy JSON, Caddyfile и Caddy Admin API payload не принимаются через
Management API. Core проверяет документ по Manifest/settings schema, но не
разбирает его продуктовые поля. Он вызывает REST `Reload(generation)`, затем
Server service получает полную revision точным config pull. Server service
семантически валидирует документ, компилирует его
во внутреннюю runtime-конфигурацию и применяет атомарно. Внутренний Caddy JSON
или Caddyfile являются производными runtime-данными и не редактируются
независимо.

Server service напрямую принимает public traffic и напрямую вызывает разрешённые
services по mTLS. Он не обращается к Management API Core за пользовательскими
запросами; Core остаётся control plane для конфигураций, grants, общих policy,
operations и audit. В текущем scope разрешён ровно один active Caddy instance, а его
ACME/site runtime state сохраняется на собственном persistent filesystem.

Settings schema Server service задаёт только public HTTP/HTTPS listeners и ordered
routes. Первый совпавший route выполняет ровно один terminal handler; route
matchers не являются middleware chain и не поддерживают `continue`.
Строгая schema не принимает Caddyfile или raw Caddy JSON. Режимы TLS —
automatic ACME, custom secret refs или явно разрешённый plaintext; HTTP/3
требует TLS. Reverse-proxy, matcher и TLS правила подробно закреплены на
[канонической странице Server service](../../services/server).

Site publication выполняется через generic Admin Surface multipart action и
Service SDK REST artifact endpoint, без буферизации artifact в Core. Возвращается `202`
с durable operation ID; точные byte/archive limits, ошибки, digest/idempotency
и `current/previous` transitions описаны в [site publishing](../api/sites).

Поведение HTTP service actions описывает owner contract Server service; общий
service lifecycle задаёт Service SDK, а peer calls — [wool source](https://github.com/Liapoldus/wool). Состав
Текущий набор проверок приведён в [acceptance matrix](acceptance), а целевая архитектура —
в [каноническом документе](../architecture/target).
