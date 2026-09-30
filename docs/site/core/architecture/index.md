# Архитектура Core

Единственная каноническая модель v1 — [целевая архитектура](target). Она
определяет singleton Core, SQLite source of truth, REST pull-based plugin
lifecycle, прямые plugin-to-plugin policies и отдельный Server plugin.

| Документ | Назначение |
| --- | --- |
| [Roadmap v1](v1-migration-roadmap) | Этапы миграции и gates. |
| [Целевые решения](target) | Нормативные роли, state, ручной startup v1 и security. |
| [Control plane](control-plane) | REST Reload/config pull, active/previous generations и internal staging для recovery. |
| [Runtime components](core) | Компактная карта владельцев. |
| [Размещение plugins](plugin-deployment) | Ручной запуск v1 и future deployment automation v2. |
| [Plugin SDK и protocol](protocol) | Разделение общего REST lifecycle SDK и generic plugin-to-plugin network. |
| [Cookies](cookies) | Plugin-owned values и общий typed boundary. |

Страница [статуса реализации](implementation) отделяет подтверждённое текущее
поведение от целевой архитектуры. При расхождении implementation не меняет
нормативный target: сначала зафиксировать gap в соответствующем TODO, затем
закрыть его тестами и реализацией по [roadmap](v1-migration-roadmap).
