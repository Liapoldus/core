# Компоненты Core

Core состоит из одного Core control-plane процесса и подключённых plugin
processes. Core сохраняет desired-конфигурацию каждого plugin в SQLite,
восстанавливает её в immutable in-memory snapshot и передаёт конкретному
instance версионированный JSON через REST `Reload` + config pull. Core не обслуживает
пользовательский traffic и не интерпретирует product-specific поля.

| Компонент | Владелец | Состояние |
| --- | --- | --- |
| Management API и CLI | Core | Settings, заранее объявленные endpoints, scoped secret grants, operations и audit в SQLite. Plugin-to-plugin policies — вне v1. |
| Plugin runtime | Operator + Plugin SDK REST | Оператор вручную запускает plugin; Core подключается по fixed endpoint и не управляет процессом или контейнером. |
| HTTP data plane | Отдельный `plugins/server` process | В v1 одна replica; Caddy входит в plugin binary, а Caddy-L4/public L4 отложены до v2. |
| Другие data-plane capabilities | Соответствующие plugins | Core видит только Manifest, schema, generic endpoint и Plugin SDK REST lifecycle. |
| Config generations | Core + Plugin SDK REST | Exact revision/digest pull и per-replica ACK; request path не читает SQLite. |

В v1 есть три сервиса (Core, Server plugin, forms-db) и две библиотеки (Plugin SDK,
`pluginprotocol`). Оператор запускает сервисы вручную. Docker/Compose, Swarm,
Kubernetes и local process supervision — v2 scope; нормативная v1 граница
описана в [целевой архитектуре](target), а будущая автоматизация помечена в
[plugin deployment](plugin-deployment).
