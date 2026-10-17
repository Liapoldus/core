# Практическая граница создания service

Общий lifecycle service-а предоставляет отдельная Go-библиотека Service SDK.
`wool` — независимая и необязательная библиотека для прямых
service-to-service вызовов. Их API не смешиваются: Service SDK не импортирует
`wool`, и сам service определяет свои Manifest, settings, capabilities,
schemas, ошибки и Admin Surface.

Service SDK существует как отдельный Go module. Утверждённый canonical import
path — `github.com/Liapoldus/service-sdk/v2`; coordinated migration текущих local
imports входит в Core v2. Normative HTTP contract и API лежат в самом SDK; этот гайд
описывает только продуктовую последовательность.
См. [границы библиотек](protocol), [целевую архитектуру](target) и
[план реализации](roadmap).

## Service control lifecycle

Оператор выбранными средствами запускает service binary. Каждая replica
аутентифицируется и регистрируется через Service SDK REST+mTLS; Core сохраняет
наблюдения, lease и конфигурационные поколения в SQLite. Core обращается к
replica по endpoint из живой регистрации. Балансируемый endpoint не заменяет
identity replica или её ACK. Core не управляет process lifecycle.

1. Оператор устанавливает и запускает Core и каждый service SDK server отдельно,
   в любом порядке. Startup/restart policy сервисов настраивается выбранными
   deployment средствами; membership появляется только после регистрации.
2. Core аутентифицирует replica, получает её Manifest и settings schema и
   сверяет release identity.
3. Core валидирует desired JSON и сохраняет точные bytes candidate в durable
   `staging` slot вместе с operation. При promotion одна транзакция удаляет
   старый `previous`, переносит прежний `active` в `previous`, а candidate — в
   `active`. `staging` нужен для recovery и не доступен service config pull.
4. Core вызывает `Reload(generation)` без конфигурационного документа. Service
   pull-ит ровно указанный generation у Core через REST, валидирует полный JSON
   и атомарно меняет in-memory config.
5. Service подтверждает generation и digest. Core допускает к traffic только
   replicas, подтвердившие текущий `active`; остальные остаются fenced и
   degraded до успешного retry.
6. После promotion Core публикует immutable snapshot и начинает Reload fan-out.
   Partial rollout идёт roll-forward;
   Rollback меняет `active`/`previous` до уведомления replicas.

Приложение не читает settings из environment, argv или собственного
application-config file. Secret references остаются в config; secret values
выдаются отдельными scoped Core REST grants. Они не должны появляться в
settings response, logs, errors, traces или audit.

## Service-to-service functionality

Сервис может использовать `wool`, если ему необходимо напрямую
вызывать другие service processes. Он регистрирует собственные arbitrary method
names и handlers. `wool` даёт общий peer call/listen/stream API,
настраиваемый physical transport/security и не знает method semantics или
product names. Изменение carrier не должно менять прикладные endpoint names и
payload contracts.

Peer calls проходят напрямую к адресату. Core публикует caller-scoped
Core-owned peer-link policy через Service SDK, а вызывающий service передаёт
разрешённый контекст generic authorizer `wool`; default — deny. Core
не proxy-ит payload и не переисполняет вызов с неизвестным результатом.

## Проверки

Каждый service владеет собственными contracts и tests для Manifest, settings,
capabilities, errors и Admin Surface. Общий SDK conformance отдельно проверяет
REST lifecycle, exact pull, Reload, rollback, health, auth и redaction.
`wool` conformance проверяет только generic registration, carriers,
peer identity/security, unary/stream cancellation, backpressure и close/reconnect.
Официальные deployment profiles Docker/Swarm/Kubernetes — v2; Core не supervises процессы,
а установку и обновления выполняет оператор.
Acceptance evidence и команды standalone-размещения собраны в
[матрице Core](../configuration/acceptance).
