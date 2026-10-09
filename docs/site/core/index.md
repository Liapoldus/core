# Liapoldus Core

Core — единичный API-only control-plane процесс. Core хранит desired service
configuration и provenance применённого bundle в SQLite; REST `Reload(generation)`
инициирует pull точного versioned JSON самим plugin.
Public traffic обслуживает отдельный Server plugin; Management API не
проксирует traffic и не встраивает Caddy.

Core принимает регистрации plugin replicas по mTLS, выдаёт leases и исключает
replica после истечения lease или отзыва identity. Первичный bootstrap задаётся
через `CORE_INIT_*` ENV; операционные настройки Core хранятся в SQLite и
меняются через versioned API. Проектными файлами, Git и публикацией bundle
управляет внешний `liapoldus` CLI.
Core не управляет процессами или контейнерами plugins и не зависит от
deployment-провайдера. Deployment profiles принимаются только после
прохождения соответствующих smoke gates.

| Область | Канон |
| --- | --- |
| Bootstrap | [ENV и SQLite settings](configuration/bootstrap) |
| Configurations | [SQLite, REST Reload и два поколения](architecture/control-plane) |
| Plugin lifecycle | [Регистрация, leases и конфигурация](architecture/plugin-deployment) |
| API | [Core Management API](api/) |
| Security/deployment | [Security](configuration/security), [Deployment](deploy/) |
| Архитектура и этапы | [Целевой контракт](architecture/target), [Roadmap v2/v3](architecture/roadmap) |
