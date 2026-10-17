# Компоненты Core v3

Core состоит из одного Core control-plane процесса и подключённых service
processes. Core сохраняет desired-конфигурацию каждого service в SQLite,
восстанавливает её в immutable in-memory snapshot и передаёт конкретному
instance версионированный JSON через REST `Reload` + config pull. Core не обслуживает
пользовательский traffic и не интерпретирует product-specific поля.

| Компонент | Владелец | Состояние |
| --- | --- | --- |
| Management API | Core | Settings, observations об аутентифицированных replica leases, scoped secret grants, operations, audit и Core-owned peer-link policy в SQLite. Membership создаётся регистрацией; standalone `liapoldus` CLI является внешним API client. |
| Service runtime | Operator + Service SDK REST | CLI/оператор запускает service; replica публикует endpoint через authenticated registration, Core не управляет процессом или контейнером. |
| HTTP data plane | Отдельный `services/server` process | Server service остаётся отдельным process; Caddy-L4/public L4 имеют отдельный product gate и не меняют Core boundary. |
| Другие data-plane capabilities | Соответствующие services | Core видит только Manifest, schema, generic endpoint и Service SDK REST lifecycle. |
| Config generations | Core + Service SDK REST | Exact revision/digest pull и per-replica ACK; request path не читает SQLite. Provenance commit/bundle/target приходит от standalone CLI через API. |

В Core v2 есть runtime Core и отдельно размещённые services; deployment workflow
выполняет standalone `liapoldus` CLI. CLI может запускать local Core, подключать
remote Core и применять одну revision к нескольким targets. Docker/Compose,
Swarm и Kubernetes остаются внешними execution adapters; Core не содержит их
управление. Нормативная граница
описана в [целевой архитектуре](target), а будущая автоматизация помечена в
[service deployment](service-deployment).
