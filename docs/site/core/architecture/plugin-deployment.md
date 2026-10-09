# Размещение plugins

## Core v1: ручное размещение

Оператор устанавливает и запускает Core и каждый plugin самостоятельно.
Оператор объявляет фиксированные REST endpoints и ожидаемые replica identities
в `core.yaml`; Core не принимает регистрацию endpoint или identity через
Management API. Core подключается к объявленным endpoints по mTLS и управляет
только generic состоянием: settings generations, Reload, health, secret grants
и audit. Централизованная plugin-to-plugin policy относится к v2. Core не
устанавливает, не запускает, не останавливает, не
перезапускает, не масштабирует и не удаляет plugin workloads.

Поддерживаемая v1-модель — отдельно установленный и вручную запускаемый plugin
process. Размещение в Docker/Compose, Swarm или Kubernetes не входит в v1
поддерживаемую матрицу, даже если workload запускается внешним оператором;
контейнерные deployment-профили проектируются отдельно в v2. При запуске Core
он однократно сверяет health/config generation у объявленных replicas и при
расхождении вызывает `Reload`. Если plugin перезапущен при работающем Core,
периодический monitor лишь отмечает его degraded; оператор после проверки
health вручную перезапускает Core. Core не запускает бинарник и не повторяет
неизвестный plugin call.

## V2: саморегистрация без управления workload

Core остаётся одним экземпляром. Оператор, systemd, Docker, Swarm или Kubernetes
запускает и масштабирует plugin workloads. Core **не** устанавливает, запускает,
перезапускает, удаляет или масштабирует их и не обращается к API платформы.
Этот принцип действует и для локального процесса. Реплики одного instance могут
жить на разных hosts, в разных Pods или совместно в одном Pod/host.

Каждая replica по Plugin SDK REST+mTLS сама регистрирует `instanceId`, уникальный
`replicaId` и incarnation, свои REST и peer endpoints, placement group, SemVer,
immutable release digest, диапазоны совместимости и подтверждённое config
generation. SAN/SPIFFE identity сертификата связывается с instance/replica;
Core проверяет её до регистрации. Digest в v2 является утверждением
аутентифицированной replica: доставку и проверку binary осуществляет оператор.
Endpoint и placement нельзя менять при renew — изменение создаёт новую
incarnation и требует drain старой. Саморегистрация не даёт права изменить
конфигурацию другого instance.

Живые endpoints и leases хранятся только в памяти Core. SQLite содержит desired
JSON generations, rollout intent, durable operations и audit, но не становится
реестром заведомо живых адресов. Lease TTL — 30 секунд, renew — каждые 10 секунд;
истечение или отрицательная health/readiness проверка исключает replica из
новых вызовов. После рестарта Core все replicas регистрируются заново. Plugin
SQLite сохраняет только маркер, что instance перешёл на self-registration:
после рестарта его сертификат не может незаметно вернуться к статическому
endpoint resolver. Этот маркер не содержит endpoint, identity, readiness или
lease и не заменяет повторную регистрацию. SDK получает versioned
peer-directory и обновления через защищённый long-poll;
изменение набора реплик не создаёт новое поколение product settings. При
недоступности Core новый rollout невозможен, но уже открытые data-plane
соединения не проксируются через Core и не переигрываются.

## V2: единая модель rollout

Единица rollout — пара `(release digest, config generation)`. Core назначает
replicas в старую и новую когорты, вызывает REST `Reload`, сверяет ACK и
readiness; не подтвердившие нужное поколение replicas fenced. Plugin SDK
Manifest сообщает совместимые диапазоны config schema, durable state и peer
contracts. Если две когорты не могут безопасно пользоваться одним состоянием,
rolling/canary отклоняется до изменения трафика.

Registration передаёт Core generic `advertisedContracts` и `acceptedContracts`:
Core не интерпретирует product contract IDs, но для разных release digest
требует взаимного принятия всех версий, объявленных каждой когортой. При
одинаковом release digest claims не нужны; при разных digest и пустых claims
совместимость считается недоказанной. Текущая проверка выполняется до promotion
configuration generation; общий release rollout/traffic cohort gate остаётся
открытой задачей. Peer-directory использует только явно запрошенные route
contract IDs.

Перед началом rollout Core фиксирует целевой набор `replicaId + incarnation`.
В выбранной когорте Core вызывает `Reload` конкурентно для всех её replicas и
собирает ACK/ошибку отдельно по каждому участнику; отказ одной replica не
задерживает вызовы остальным. Для стратегии `immediate` целевой набор включает
все совместимые регистрации instance с действующей mTLS identity и
неистёкшей lease, в том числе replicas, ещё не объявившие `ready`: первая
конфигурация как раз переводит такую replica в readiness. `ready` определяет
допуск к traffic, а не исключение из config rollout. Для rolling/canary набор
включает только назначенную совместимую rollout-когорту. Новая incarnation,
зарегистрированная после фиксации набора, не подменяет участника начатой операции и не получает доступ
к трафику, пока не подтвердит active release/config generation. Неответившие
или отказавшие участники остаются fenced/degraded; Core выполняет roll-forward
той же immutable generation без повторного выполнения пользовательских
операций. Exact target остаётся retryable до истечения lease. Истечение
зафиксированной lease либо регистрация другой incarnation с тем же replica ID
терминально завершает operation как `failed/target_lost` и закрывает rollout;
active generation остаётся desired state, replacement не подставляется, а
повтор требует новой явной операции. Для живой lease Core периодически сверяет точный active generation и
повторно объявляет только отстающим replicas тот же идемпотентный `Reload`;
просроченные leases исключаются, а результаты записываются отдельно на replica.
Поздний ACK не меняет исход уже зафиксированного HTTP-запроса: durable rollout
operation остаётся источником статуса и должна завершиться по своему ACK barrier.
Сначала закрывается текущий набор ACK/ошибок, затем допускается
следующий шаг traffic-controller.

При изменении конфигурации новый generation после полной валидации становится
`active`, прежний — `previous`, `staging` очищается. Только пока открыт rollout,
обе когорты допускаются к трафику. По завершении `previous` снова доступен
лишь для rollback. Одновременный второй config rollout запрещён. Одна replica
обслуживает одно поколение; rolling и процентный canary требуют минимум две
готовые когорты. Для одной replica допустим immediate с окном простоя, без
молчаливой подмены выбранной стратегии.

Rollout intent задаёт явные ступени веса, минимальное время наблюдения и
необязательное ручное подтверждение. Core управляет Reload/ACK, но **не**
публичным traffic. Внешний controller читает intent через защищённый API,
применяет веса и возвращает подтверждённый результат: в Kubernetes это может
быть Gateway API, в Swarm — ingress с поддержкой весов, standalone использует
операторский adapter без процентного canary. Если controller не подтвердил
новый вес либо health ухудшился, Core останавливает продвижение на последнем
подтверждённом весе; автоматического rollback/replay нет. Sticky для клиентов
обеспечивает ingress; существующие streams доживают либо drain-ятся до
установленного deadline. Межплагинный traffic использует generic SDK resolver
с весами когорт и необязательным стабильным routing key.

Внешний controller подключается к отдельному private mTLS listener Core, с
собственными server/client trust roots и allow-list точных certificate
identities. Этот listener не принимает Bearer или plaintext. Controller получает
только права читать rollout intent и подтверждать фактически применённый вес;
`platform-admin` Bearer token не требуется и не выдаёт controller-доступа.
Identity проверяется до чтения или записи, ACK проходит revision/CAS-проверку и
audit. Controller не может менять rollout plan, plugin settings, peer links,
identities или service keys; неизвестная либо отозванная identity отклоняется.
Trust roots controller listener не объединяются с Management API, Plugin SDK
REST или peer-protocol trust roots. Bind, server credentials, client CA, CRL и
точные allowed identities задаются отдельным versioned v2 документом
`contracts/v2/traffic-controller.schema.json`, передаваемым Core при запуске
через отдельный `--traffic-controller-config`. Этот документ не расширяет
минимальный v1 `core.yaml`.

Platform-admin создаёт intent через `POST /api/plugins/{id}/rollouts`. Запрос
содержит desired release SHA-256, точный JSON candidate document, ожидаемую
active revision, список stage-ов и точные пары `replicaId + incarnation` для
candidate cohort. Core до изменения состояния проверяет, что каждая выбранная
replica зарегистрирована, имеет действующую lease, совпадает с desired release
digest и совместима с конфигурацией; остальные участвующие replicas должны
образовывать одну совместимую incumbent cohort. Core фиксирует состав обеих
когорт и план ступеней в SQLite, валидирует generic schema, затем атомарно
продвигает candidate в `active` при начале roll-forward. Обычный
`PUT /api/plugins/{id}/settings` сохраняет immediate-семантику и не создаёт
traffic rollout. Повторный открытый rollout для instance отклоняется.
Core отправляет `Reload` только точным candidate incarnation и сохраняет ACK
каждой replica в SQLite. После частичного ACK процесс повторяет только
неподтверждённые цели; incumbent остаётся на прежней конфигурации. Потеря
выбранной incarnation закрывает rollout с `target_lost`, не подменяя её новой.
Traffic-controller не должен получать intent и не может подтвердить вес,
пока candidate config ACK barrier не закрыт.
Пока последний traffic rollout не завершён, обычный settings update, rollback
и фоновая рассылка active generation не меняют закреплённые когорты. Ошибка
`target_lost` оставляет instance ограждённым от generic fanout; восстановление
требует новой явной rollout-операции. После подтверждённого завершения rollout
обычная config reconciliation снова доступна.
Список candidate targets передаёт platform-admin; Core сохраняет его как точные
incarnation IDs и не заменяет исчезнувшую replica новой. Для каждого stage
ручное одобрение обязательно: пропущенное `requireManualApproval` означает
`true`, явное `false` отклоняется. Stage продвигается только после отдельного
platform-admin `POST .../stages/{stageId}/approve`; stage ID ограничен ASCII
`A-Z`, `a-z`, `0-9`, `.`, `_`, `-` (до 128 символов), чтобы адресоваться одним
сегментом URL. Запрос требует `If-Match` revision rollout и `Idempotency-Key`.
Текущую revision и состояние platform-admin получает через
`GET /api/plugins/{id}/rollouts/{rolloutId}`; ответ содержит strong `ETag`,
который используется в approval.
Core одной SQLite-транзакцией фиксирует approval, audit, продвижение следующего
stage или завершение rollout и completed operation. Повтор того же ключа
возвращает ту же operation без повторного продвижения. Controller ACK сам по
себе не обходит это требование. Несовпадение revision/state или незавершённое
окно наблюдения возвращает `412`; конфликт повторного ключа — `409`. Короткая
операция отвечает `200` после commit.

Traffic-controller использует только свой listener: `GET
/internal/v2/traffic-rollouts` возвращает intent-ы после закрытия точного
candidate config ACK-барьера. Ответ содержит общий ETag списка и отдельную
revision каждого rollout; `If-None-Match` позволяет не перечитывать неизменившийся
список. `PUT /internal/v2/traffic-rollouts/{id}/confirmation` требует strong
`If-Match` с revision этого rollout и `Idempotency-Key`; JSON сообщает `stageId`,
фактически применённый вес и opaque revision самого controller. Core принимает
лишь текущий stage и точный вес. Точный повтор того же ключа и request digest
возвращает сохранённый receipt; тот же ключ с другим запросом получает `409`.
ACK, receipt и audit фиксируются одной SQLite-транзакцией. Устаревшая revision
или out-of-order stage отклоняется без изменения состояния. Продвижение разрешено
только отдельным platform-admin approval после подтверждения веса и истечения
минимального времени наблюдения. Controller никогда сам не одобряет следующую
ступень. Listener подключается к `core serve` через отдельный
`--traffic-controller-config`, использует собственные server/client trust roots,
CRL и точную identity allow-list; Bearer и plaintext не принимаются.

## V2: явные peer transports и смешанное размещение

Core хранит deny-by-default caller→target link policy. Для одной пары можно
явно задать отдельные правила `same-placement` и `remote`; каждое называет
transport: TCP, QUIC, Unix socket на Linux/macOS либо Windows named pipe на
одном host. SDK публикует разрешённые endpoints и weights в peer-directory,
а generic `pluginprotocol` только устанавливает выбранное peer-соединение и
исполняет вызов. SDK не импортирует `pluginprotocol`; Core не обрабатывает
peer payload. При отсутствии локальной target replica socket-only вызов
возвращает bounded unavailable. Неявного перехода на TCP/QUIC нет.

mTLS и проверка peer identity обязательны также поверх socket/pipe; права
файла и pipe ACL служат дополнительной защитой. Совместно размещённые plugins
в Kubernetes Pod могут разделять каталог socket через volume; контейнерный
Windows named-pipe профиль нельзя объявлять поддерживаемым до отдельного
conformance. Меж-Pod соединение использует явно заданное сетевое правило.

Источник desired policy — SQLite Core, а не `core.yaml` и не статический
contract asset. Management API предоставляет список и ресурс пары
`callerInstanceId/targetInstanceId`; `PUT` целиком заменяет набор правил пары,
`DELETE` удаляет его. Каждая запись имеет монотонную revision и strong ETag:
`POST /api/plugin-links` создаёт новую пару и отвечает `409`, если она уже
существует. `If-Match` обязателен при замене/удалении. Все мутации требуют
`Idempotency-Key`, проходят generic
валидацию Core, CAS и audit в одной SQLite-транзакции; доступен только
`platform-admin`. Операции можно подготовить до регистрации target instance.
Отсутствие записи означает deny; пустой набор и дублирующиеся комбинации
placement/carrier отклоняются.

После commit Core атомарно публикует новый immutable in-memory policy snapshot
и будит ожидающие directory watches. На старте snapshot восстанавливается из
SQLite. `GET /internal/v2/plugin-peer-directory` использует SDK-defined
long-poll: ответ scoped к аутентифицированной caller replica и содержит только
разрешённые links. Watch просыпается при изменении policy, регистрации,
readiness или удалении/истечении lease; обработчик также ждёт ближайшего
релевантного lease deadline, поэтому для точного expiry не нужен отдельный
периодический reaper. Точная форма cursor, timeout и ответа следует owner
контракту Plugin SDK. Core не хранит live endpoints в SQLite и не передаёт
peer payload.

## Операторское размещение

Оператор устанавливает и обновляет Core и plugins выбранными средствами.
Доставка, проверка artifact digest, bootstrap-параметры, сертификаты и workloads
принадлежат оператору; product JSON и поколения остаются в Core.
Provider credentials не передаются Core. Проверка происхождения
artifact принадлежит оператору и его CI/registry. Сообщённый replica release
digest Core применяет лишь для совместимости когорт; он не доказывает, какой
binary был установлен.

При плановом обновлении оператор поднимает candidate workload с новой
incarnation. После self-registration platform-admin создаёт rollout с точным
списком candidate replicas; Core выполняет `Reload` и хранит ACK. Внешний
traffic controller подтверждает применённый вес, а platform-admin отдельно
одобряет следующую ступень. Старый workload выводится только после завершения
rollout. Если workload исчез, lease исключает его из новых вызовов, но desired
config в Core не удаляется. Core не устанавливает, не удаляет и не масштабирует
workloads ни в одной версии.

V3 может вместо отдельных Go-plugin workloads использовать статически
скомпонованный единый Core executable. Сборка задаётся composition root,
а доставка и запуск artifact не требуют инфраструктурной автоматизации;
подробности — в [монолитной композиции v3](target#v3-c-abi-и-монолитная-композиция).

Полная целевая архитектура и текущие gates описаны в
[архитектуре Core](target) и [acceptance matrix](../configuration/acceptance).
