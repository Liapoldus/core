# Компоненты Core v2

Core состоит из одного Core control-plane процесса и подключённых plugin
processes. Core сохраняет desired-конфигурацию каждого plugin в SQLite,
восстанавливает её в immutable in-memory snapshot и передаёт конкретному
instance версионированный JSON через REST `Reload` + config pull. Core не обслуживает
пользовательский traffic и не интерпретирует product-specific поля.

| Компонент | Владелец | Состояние |
| --- | --- | --- |
| Management API | Core | Settings, observations об аутентифицированных replica leases, scoped secret grants, operations и audit в SQLite. Membership создаётся регистрацией; plugin-to-plugin policies принадлежат pluginprotocol. Standalone `liapoldus` CLI является внешним API client. |
| Plugin runtime | Operator + Plugin SDK REST | CLI/оператор запускает plugin; replica публикует endpoint через authenticated registration, Core не управляет процессом или контейнером. |
| HTTP data plane | Отдельный `plugins/server` process | В v2 Server plugin остаётся отдельным process; Caddy-L4/public L4 отложены до v3. |
| Другие data-plane capabilities | Соответствующие plugins | Core видит только Manifest, schema, generic endpoint и Plugin SDK REST lifecycle. |
| Config generations | Core + Plugin SDK REST | Exact revision/digest pull и per-replica ACK; request path не читает SQLite. Provenance commit/bundle/target приходит от standalone CLI через API. |

В Core v2 есть runtime Core и отдельно размещённые plugins; deployment workflow
выполняет standalone `liapoldus` CLI. CLI может запускать local Core, подключать
remote Core и применять одну revision к нескольким targets. Docker/Compose,
Swarm и Kubernetes остаются внешними execution adapters; Core не содержит их
управление. Нормативная граница
описана в [целевой архитектуре](target), а будущая автоматизация помечена в
[plugin deployment](plugin-deployment).
