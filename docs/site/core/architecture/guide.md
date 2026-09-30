# Практическая граница создания plugin

Общий lifecycle plugin-а предоставляет отдельная Go-библиотека Plugin SDK.
`pluginprotocol` — независимая и необязательная библиотека для прямых
plugin-to-plugin вызовов. Их API не смешиваются: Plugin SDK не импортирует
`pluginprotocol`, и сам plugin определяет свои Manifest, settings, capabilities,
schemas, ошибки и Admin Surface.

Plugin SDK уже существует как отдельный локальный Go module; его временный
module path пока не является публикуемым canonical path. Normative HTTP contract
и API лежат в самом SDK; этот гайд описывает только продуктовую последовательность.
См. [границы библиотек](protocol), [целевую архитектуру](target) и
[план перехода](v1-migration-roadmap).

## Plugin control lifecycle

В v1 оператор вручную запускает plugin binary и регистрирует его fixed endpoint
в Core. Plugin предоставляет общий защищённый REST control surface SDK; Core
обращается к каждой replica отдельно. Балансируемый endpoint не заменяет
identity replica или её ACK. Core не управляет process/container lifecycle.

1. Оператор устанавливает и вручную запускает Core, затем каждый plugin SDK
   server отдельно. Он настраивает startup/restart policy средствами ОС.
2. Core аутентифицирует replica, получает её Manifest и settings schema и
   сверяет release identity.
3. Core валидирует desired JSON и сохраняет точные bytes candidate в durable
   `staging` slot вместе с operation. При promotion одна транзакция удаляет
   старый `previous`, переносит прежний `active` в `previous`, а candidate — в
   `active`. `staging` нужен для recovery и не доступен plugin config pull.
4. Core вызывает `Reload(generation)` без конфигурационного документа. Plugin
   pull-ит ровно указанный generation у Core через REST, валидирует полный JSON
   и атомарно меняет in-memory config.
5. Plugin подтверждает generation и digest. Core допускает к traffic только
   replicas, подтвердившие текущий `active`; остальные остаются fenced и
   degraded до успешного retry.
6. После promotion Core публикует immutable snapshot и начинает Reload fan-out.
   Partial rollout идёт roll-forward;
   Rollback меняет `active`/`previous` до уведомления replicas.

Приложение не читает settings из environment, argv или собственного
application-config file. Secret references остаются в config; secret values
выдаются отдельными scoped Core REST grants. Они не должны появляться в
settings response, logs, errors, traces или audit.

## Plugin-to-plugin functionality

Плагин может использовать `pluginprotocol`, если ему необходимо напрямую
вызывать другие plugin processes. Он регистрирует собственные arbitrary method
names и handlers. `pluginprotocol` даёт общий peer call/listen/stream API,
настраиваемый physical transport/security и не знает method semantics или
product names. Изменение carrier не должно менять прикладные endpoint names и
payload contracts.

Peer calls проходят напрямую к адресату по deny-by-default policy, которую Core
администрирует отдельно через Plugin SDK REST. Core не proxy-ит payload и не
переисполняет вызов с неизвестным результатом. Opaque one-use grant, если он
нужен вызову, выпускается/погашается у Core REST и лишь переносится peer
transport как opaque metadata.

## Проверки

Каждый plugin владеет собственными contracts и tests для Manifest, settings,
capabilities, errors и Admin Surface. Общий SDK conformance отдельно проверяет
REST lifecycle, exact pull, Reload, rollback, health, auth и redaction.
`pluginprotocol` conformance проверяет только generic registration, carriers,
peer identity/security, unary/stream cancellation, backpressure и close/reconnect.
Docker/Compose, Swarm, Kubernetes и Core process supervision — v2 scope.
Acceptance evidence и команды standalone-размещения собраны в
[матрице Core](../configuration/acceptance).
