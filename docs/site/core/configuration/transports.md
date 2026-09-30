# Public transports

HTTP/1.1, HTTP/2/3, TLS, WebSocket и SSE обслуживает отдельный
`plugins/server` process на базе Caddy. В v1 у одного Core допускается ровно
одна active Server replica. Core не принимает public requests и не реализует
traffic sockets.

Caddy-specific JSON settings хранятся в Core SQLite и выдаются через Plugin SDK
REST config pull после `Reload`; Server plugin преобразует их в runtime-конфигурацию. Это не
Caddyfile API и не вторая Core route DSL. Публичные порты открывает сам
plugin. Оператор вручную запускает Caddy с необходимыми OS permissions и
обеспечивает публикацию портов; Core не открывает public sockets и не
управляет listener/container resources. Docker/Compose, Swarm, Kubernetes и
local process supervision отложены до v2.

Публичные TCP/UDP listeners, relay, Caddy-L4 и P2P не входят в v1 и перенесены
в v2. Транспорт TCP или QUIC внутри `pluginprotocol` — отдельная внутренняя
сеть plugin↔plugin и не означает наличие публичного L4 data plane. См.
[Acceptance](acceptance) и [целевую архитектуру](../architecture/target).
