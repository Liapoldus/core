# Liapoldus Core

Core — единичный control-plane процесс. Core хранит desired service
configuration в SQLite; REST `Reload(generation)` инициирует pull точного
versioned JSON самим plugin.
Public traffic обслуживает отдельный Server plugin; Management API не
проксирует traffic и не встраивает Caddy.

v1 включает ровно три сервиса — Core, Server plugin и forms-db plugin — и две
библиотеки: Plugin SDK и `pluginprotocol`. Оператор вручную устанавливает и
запускает все три сервиса; Core только подключается к заранее настроенным
plugin endpoints и не управляет их процессами или контейнерами. Docker/Compose,
Swarm, Kubernetes и process supervision перенесены в v2. Constructor остаётся
отдельным замороженным продуктом.

| Область | Канон |
| --- | --- |
| Bootstrap | [Минимальный `core.yaml`](configuration/yaml-reference) |
| Configurations | [SQLite, REST Reload и два поколения](architecture/control-plane) |
| Plugin startup | [Ручное размещение v1 и будущая автоматизация v2](architecture/plugin-deployment) |
| API | [Core Management API](api/) |
| Security/deployment | [Security](configuration/security), [Deployment](deploy/) |
| Архитектура и этапы | [Целевой контракт](architecture/target), [Roadmap](architecture/v1-migration-roadmap) |
