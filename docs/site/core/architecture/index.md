# Архитектура Core

Каноническая модель — [целевая архитектура](target). Она определяет singleton
Core API, SQLite source of truth, REST pull-based service lifecycle, прямые
service-to-service policies и отдельный Server service. Проектом и deployment
управляет внешний standalone CLI; Core не содержит пользовательскую CLI.

| Документ | Назначение |
| --- | --- |
| [Roadmap v2/v3](roadmap) | Согласованные этапы и межрепозиторные gates. |
| [Целевые решения](target) | Нормативные роли, state и security. |
| [Control plane](control-plane) | REST Reload/config pull, active/previous generations и internal staging для recovery. |
| [Runtime components](core) | Компактная карта владельцев. |
| [Размещение services](service-deployment) | Регистрация, leases, rollout и ownership workloads. |
| [Service SDK и protocol](protocol) | Разделение общего REST lifecycle SDK и generic service-to-service network. |
| [Целевая архитектура v3](v3) | Единый breaking-контракт, ownership, runtime-профили и production gates. |
| [Cookies](cookies) | Service-owned values и общий typed boundary. |

Страница [статуса реализации](implementation) отделяет подтверждённое текущее
поведение от целевой архитектуры. При расхождении implementation не меняет
нормативный target: сначала зафиксировать gap в соответствующем TODO, затем
закрыть его тестами и реализацией по [roadmap](roadmap).
