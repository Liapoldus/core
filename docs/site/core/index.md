# Liapoldus Core

Core — единичный API-only control-plane процесс. Core хранит desired service
configuration и provenance применённого bundle в SQLite; REST `Reload(generation)`
инициирует pull точного versioned JSON самим service.
Public traffic обслуживает отдельный Server service; Management API не
проксирует traffic и не встраивает Caddy.

Core принимает регистрации service replicas по mTLS, выдаёт leases и исключает
replica после истечения lease или отзыва identity. Первичный bootstrap задаётся
через одноразовый bootstrap, запускаемый standalone `liapoldus` CLI; операционные настройки Core хранятся в SQLite и
меняются через versioned API. Проектными файлами, Git и публикацией bundle
управляет внешний `liapoldus` CLI.
Core не управляет процессами или контейнерами services и не зависит от
deployment-провайдера. Deployment profiles принимаются только после
прохождения соответствующих smoke gates.

| Область | Канон |
| --- | --- |
| Bootstrap | [ENV и SQLite settings](configuration/bootstrap) |
| Configurations | [SQLite, REST Reload и два поколения](architecture/control-plane) |
| Service lifecycle | [Регистрация, leases и конфигурация](architecture/service-deployment) |
| API | [Core Management API](api/) |
| Security/deployment | [Security](configuration/security), [Deployment](deploy/) |
| Архитектура и этапы | [Целевая архитектура v3](architecture/v3), [Целевой контракт](architecture/target), [Roadmap v2/v3](architecture/roadmap) |
