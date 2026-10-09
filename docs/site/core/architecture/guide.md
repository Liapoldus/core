# Практическая граница создания plugin

Общий lifecycle plugin-а предоставляет отдельная Go-библиотека Plugin SDK.
`pluginprotocol` — независимая и необязательная библиотека для прямых
plugin-to-plugin вызовов. Их API не смешиваются: Plugin SDK не импортирует
`pluginprotocol`, и сам plugin определяет свои Manifest, settings, capabilities,
schemas, ошибки и Admin Surface.

Plugin SDK существует как отдельный Go module. Утверждённый canonical import
path — `github.com/Liapoldus/plugin-sdk`; coordinated migration текущих local
imports входит в Core v2. Normative HTTP contract и API лежат в самом SDK; этот гайд
описывает только продуктовую последовательность.
См. [границы библиотек](protocol), [целевую архитектуру](target) и
[план реализации](roadmap).

## Plugin control lifecycle

Оператор выбранными средствами запускает plugin binary. Каждая replica
аутентифицируется и регистрируется через Plugin SDK REST+mTLS; Core сохраняет
наблюдения, lease и конфигурационные поколения в SQLite. Core обращается к
replica по endpoint из живой регистрации. Балансируемый endpoint не заменяет
identity replica или её ACK. Core не управляет process lifecycle.

1. Оператор устанавливает и запускает Core и каждый plugin SDK server отдельно,
   в любом порядке. Startup/restart policy плагинов настраивается выбранными
   deployment средствами; membership появляется только после регистрации.
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

Peer calls проходят напрямую к адресату. Решение о разрешении вызова принимает
вызывающий plugin через собственную authorization policy и generic authorizer
`pluginprotocol`; Core не хранит и не администрирует plugin-to-plugin policy,
не proxy-ит payload и не переисполняет вызов с неизвестным результатом.
Централизованные peer policies и grants отложены до v2.

## Проверки

Каждый plugin владеет собственными contracts и tests для Manifest, settings,
capabilities, errors и Admin Surface. Общий SDK conformance отдельно проверяет
REST lifecycle, exact pull, Reload, rollback, health, auth и redaction.
`pluginprotocol` conformance проверяет только generic registration, carriers,
peer identity/security, unary/stream cancellation, backpressure и close/reconnect.
Официальные deployment profiles Docker/Swarm/Kubernetes — v2; Core не supervises процессы,
а установку и обновления выполняет оператор.
Acceptance evidence и команды standalone-размещения собраны в
[матрице Core](../configuration/acceptance).
