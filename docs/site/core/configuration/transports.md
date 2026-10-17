# Public transports

HTTP/1.1, HTTP/2/3, TLS, WebSocket и SSE обслуживает отдельный
`services/server` process на базе Caddy. В текущем scope у одного Core допускается ровно
одна active Server replica. Core не принимает public requests и не реализует
traffic sockets.

Caddy-specific JSON settings хранятся в Core SQLite и выдаются через Service SDK
REST config pull после `Reload`; Server service преобразует их в runtime-конфигурацию. Это не
Caddyfile API и не вторая Core route DSL. Публичные порты открывает сам
service. Core не открывает public sockets и не управляет listener/container
resources. Deployment automation обращается к Core API и внешним providers
через самостоятельные адаптеры. Поддерживаемыми
считаются только профили, прошедшие отдельные native smoke gates; наличие
playbook или manifest само по себе support не подтверждает.

Публичные TCP/UDP listeners, relay, Caddy-L4 и P2P не входят в текущий Core scope и
отложены до следующего этапа. Транспорт TCP или QUIC внутри `wool` — отдельная внутренняя
сеть service↔service и не означает наличие публичного L4 data plane. См.
[Acceptance](acceptance) и [целевую архитектуру](../architecture/target).
