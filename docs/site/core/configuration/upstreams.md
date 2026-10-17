# Upstream и balancing

Core не имеет собственной модели upstream-ов и не принимает native Caddyfile.
Traffic settings задаются service-owned strict JSON schema, Core хранит полную
desired revision в SQLite, а Server service компилирует её в runtime
configuration. Допустимые origin, веса, балансировка, пассивное исключение,
TLS verification и безопасный retry определены в [контракте Server service](/services/server).
Отдельной Core upstream DSL нет.

Service-to-service вызовы идут напрямую через `wool`; Core не
маршрутизирует payload и не хранит interaction policy в текущем базовом scope. Разрешения peer
вызова принадлежат service-owned configuration и локальному policy consumer-а.
См. [транспорты](transports) и [границы библиотек](../architecture/protocol).
