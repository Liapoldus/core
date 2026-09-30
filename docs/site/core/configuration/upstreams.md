# Upstream и balancing

Core не имеет собственной модели upstream-ов и не принимает native Caddyfile.
Traffic settings задаются plugin-owned strict JSON schema, Core хранит полную
desired revision в SQLite, а Server plugin компилирует её в runtime
configuration. Допустимые origin, веса, балансировка, пассивное исключение,
TLS verification и безопасный retry определены в [контракте Server plugin](/plugins/server).
Отдельной Core upstream DSL нет.

Plugin-to-plugin вызовы идут напрямую через `pluginprotocol`; Core не
маршрутизирует payload и не хранит interaction policy в v1. Разрешения peer
вызова принадлежат plugin-owned configuration и локальному policy consumer-а.
См. [транспорты](transports) и [границы библиотек](../architecture/protocol).
