# Архитектура Core

Каноническая модель — [целевая архитектура](target). Она определяет singleton
Core, SQLite source of truth, REST pull-based plugin lifecycle, прямые
plugin-to-plugin policies и отдельный Server plugin.

| Документ | Назначение |
| --- | --- |
| [Roadmap v2/v3](v1-migration-roadmap) | Согласованные этапы и межрепозиторные gates. |
| [Целевые решения](target) | Нормативные роли, state и security. |
| [Control plane](control-plane) | REST Reload/config pull, active/previous generations и internal staging для recovery. |
| [Runtime components](core) | Компактная карта владельцев. |
| [Размещение plugins](plugin-deployment) | Регистрация, leases, rollout и ownership workloads. |
| [Plugin SDK и protocol](protocol) | Разделение общего REST lifecycle SDK и generic plugin-to-plugin network. |
| [Cookies](cookies) | Plugin-owned values и общий typed boundary. |

Страница [статуса реализации](implementation) отделяет подтверждённое текущее
поведение от целевой архитектуры. При расхождении implementation не меняет
нормативный target: сначала зафиксировать gap в соответствующем TODO, затем
закрыть его тестами и реализацией по [roadmap](v1-migration-roadmap).
