# Control plane: SQLite, конфигурации и поколения

Эта страница описывает долговременное состояние Core и доставку конфигурации
плагинам. Core всегда один; SQLite — единственная долговременная база Core и
источник desired configuration. Каноническое распределение ответственности
задано в [целевой архитектуре](target), а общий REST plugin contract принадлежит
отдельной библиотеке Plugin SDK. `pluginprotocol` в этой модели не участвует в
управлении Core и используется только для plugin-to-plugin взаимодействия.

## Состояние Core в v2

SQLite хранит только control-plane state, нужный для восстановления и объяснения
операций:

| Данные | Содержимое | Не хранить |
| --- | --- | --- |
| Plugin instance | Стабильный ID, plugin identity, зарегистрированные replicas и timestamps. | Product-specific поля, binaries и process lifecycle. |
| Replica membership | Стабильный ID replica, отдельный REST control endpoint, ожидаемая mTLS identity и desired/observed status. | Private keys, bearer tokens и TLS secret bytes. |
| Config generations | До одной строки на instance/slot: `instance_id`, монотонный `generation`, `slot`, точный `raw_json BLOB`, `sha256`, `schema_version`, `created_at`. Слоты: `active`, `previous` и непубликуемый `staging`. | Product-specific распарсенные поля и раскрытые секреты. |
| Frozen rollout cohort | `plugin_rollouts`: operation, instance, generation и open/completed state; `plugin_rollout_targets`: точные `replica_id`, `incarnation_id`, release digest и per-target ACK. Membership неизменна после promotion. | Live endpoints, leases и credentials. |
| Replica observations | Config generation, digest, replica identity и время наблюдения/подтверждения. Rollout ACK принадлежит неизменяемому target row. | Credentials целиком. |
| Operations/idempotency | Тип операции, safe resource IDs, состояние, input digest, результат и timestamps. | Чувствительные request/response body. |
| Access/audit | Actor, authorization result, mutation/resource и before/after digest. | Повторно выдаваемые tokens, cookies, authorization и secret values. |

SQLite — долговременное хранилище desired state и журнала операций. При старте
Core проверяет БД и контрольные данные, восстанавливает подтверждённое состояние,
строит immutable in-memory snapshot и использует snapshot на runtime-пути.
Обработка обычного plugin request не читает SQLite. Плагины применяют настройки
в собственную память и не обязаны иметь локальный application-config file.

### Владение данными

- Core SQLite: desired JSON generations, replica endpoints/identities, scoped
  secret-grant metadata, operations, audit и active generation. Peer policies
  и plugin-to-plugin grants не входят в текущий v2 scope.
- Plugin binaries: оператор устанавливает, запускает, обновляет и резервирует
  их отдельно; Core не хранит package store и не имеет process-control API.
- Plugin storage: продуктовые данные и runtime artifacts. Server plugin отдельно
  хранит ACME state, сертификаты и site releases на своём persistent volume.
- Внешний secret provider: secret bytes. Core и плагины сохраняют ссылки и
  ограниченные grant metadata, но не логируют раскрытые значения.

### Raw JSON и физическая модель поколений

Единственное хранилище plugin configuration — таблица
`plugin_config_generations` со столбцами `instance_id`, `generation`, `slot`,
`raw_json BLOB`, `sha256`, `schema_version` и `created_at`. Ограничение
уникальности `(instance_id, slot)` допускает не более одной строки каждого
слота; `slot` принимает `active`, `previous` или `staging`. Generation
уникален и монотонно увеличивается в пределах instance. Пустой slot представлен
отсутствующей строкой.

Management `PUT` принимает непосредственно JSON object plugin settings — без
общей `{ "config": ... }` оболочки. До сохранения Core проверяет верхний лимит
размера, корректный UTF-8, JSON object syntax, отсутствие дублирующихся ключей
на любой глубине и generic JSON Schema подключённого plugin. Затем сохраняются
исходные bytes: decode/remarshal, каноникализация, сортировка ключей и изменение
whitespace запрещены. SHA-256 считается по точному `raw_json`; config pull
возвращает те же bytes, schema version, generation и digest. Парсинг для
валидации не становится представлением, из которого конфигурация записывается.

Durable operation хранит только instance ID, generation, digest, schema version,
actor/idempotency metadata и состояние; JSON payload отдельно в операции не
дублируется. Ошибка до promotion не меняет `active`/`previous` и не вызывает
`Reload`.

Для зарегистрированного instance promotion и фиксация rollout cohort происходят
в одной SQLite transaction. Recovery читает сохранённые `(replica_id,
incarnation_id, release_sha256)` и вызывает `Reload` только для точного живого
участника; новая incarnation не подтверждает старую, а уже сохранённый ACK не
переигрывается. Пока cohort не сошёлся, operation остаётся открытой, instance
остаётся fenced для конфликтующих mutations, а второй rollout запрещён. Прямой
`RestorePrevious` также отклоняется до изменения pointers при открытом rollout
или staging-кандидате. API rollback сериализуется тем же application lock, что
и config apply, на весь swap и `Reload`. В текущем Core WIP rollback для
зарегистрированного instance в одной SQLite-транзакции меняет `active` и
`previous` и фиксирует собственную exact-incarnation cohort; partial ACK и
recovery обрабатываются как durable operation без повторного вызова уже
подтвердивших replicas. Это покрыто `registered-rollout-cohort.test.ts` и
`plugin-settings-rollback.test.ts`. Данный локальный результат не закрывает
release-compatible Core/SDK build и полный hosted release gate.

## Два поколения конфигурации

Для одного plugin instance Core хранит не более двух полных версий настроек:

| Слот | Назначение | Изменение |
| --- | --- | --- |
| `active` | Желаемое поколение, которое Core раскатывает и требует от replicas. | Core продвигает проверенный candidate в `active` до уведомления replicas. |
| `previous` | Последнее поколение, от которого можно выполнить rollback. | При продвижении candidate замещается бывшим `active`. |
| `staging` | Проверенный candidate, сохранённый вместе с durable operation для crash recovery. | Не выдаётся config pull; validation failure не создаёт slot. Promotion или отказ очищают либо перемещают его транзакционно. |

Новая management mutation требует authentication, authorization, idempotency и
CAS (`If-Match`). Core проверяет JSON Schema и целостность всего документа,
затем одной транзакцией меняет `active` и `previous`, фиксирует intent/audit,
digest и durable operation до внешнего вызова. Пока rollout не завершён,
конфликтующие mutations запрещены. Secret values не включаются в документ; разрешены только opaque
references.

## REST pull и Reload

Общий REST control contract задаёт Plugin SDK; его endpoint shapes и ошибки не
дублируются этой страницей. Последовательность изменения настроек такова:

1. Core ограниченно принимает и валидирует candidate в памяти; ошибка до полной
   validation не меняет durable slots и не вызывает `Reload`.
2. После полной validation Core одной SQLite transaction сохраняет точные
   candidate bytes в `staging` и связывает их с durable operation. До promotion
   прежние `active`/`previous` остаются неизменными.
3. Promotion одной SQLite transaction удаляет прежний `previous`, переносит
   текущий `active` в `previous`, а candidate из `staging` — в `active`, фиксирует
   rollout state и публикует новое immutable in-memory generation.
4. Для каждой обязательной replica Core вызывает REST `Reload` с целевым
   generation и семантикой «эта версия доступна и должна стать active». Это
   уведомление, а не передача настроек: в запросе нет конфигурационного
   документа. Точная JSON-форма принадлежит Plugin SDK.
5. Плагин сам обращается к защищённому Core REST endpoint и запрашивает ровно
   указанный immutable generation. Core авторизует конкретные instance/replica,
   operation и generation; ответ содержит versioned JSON, schema version и
   digest.
6. Плагин валидирует весь документ и атомарно меняет собственную in-memory
   конфигурацию. Он возвращает Core подтверждение точных generation и digest.
   Если проверка или применение не прошли, локально остаётся его прежняя
   конфигурация.
7. Когда все обязательные replicas подтвердили target generation, Core
   завершает operation как `succeeded`. Если начальная попытка Reload завершилась
   отказом, operation становится `failed` с безопасным кодом ошибки; желаемый
   `active` не откатывается. Instance остаётся degraded, а traffic разрешён
   только через replicas, подтвердившие именно active generation. Readiness
   monitor только записывает наблюдения. После устранения причины оператор
   вручную перезапускает Core, чтобы выполнить startup reconciliation; явный
   rollback создаёт новую operation.

`Reload` идемпотентен по instance, replica и generation. Повторный config pull
`GET` возвращает тот же неизменяемый документ; Core не подменяет содержимое уже
выданного generation. Плагин не опрашивает Core постоянно: он получает
инициирующий Reload, а конфигурацию забирает сам. Core периодически опрашивает
SDK readiness и фиксирует наблюдаемое состояние, но этот monitor read-only и
никогда не вызывает `Reload`. Если оператор независимо перезапустил plugin при
работающем Core, replica остаётся degraded до ручного перезапуска Core. Startup
reconciliation повторно сверяет identity и active generation и инициирует
`Reload` при расхождении. Core не перезапускает plugin и не повторяет
пользовательский Call.

## Частичный rollout и rollback в v2

Принята стратегия **roll-forward**. После продвижения candidate `active` не
возвращается автоматически к старой версии из-за частичного отказа. Подтвердившие
replicas обслуживают новый active generation; отставшие исключены из зависимого
traffic/peer calls, instance отмечен degraded. Core не повторяет `Reload` в
фоновом режиме: периодический monitor только читает readiness. После исправления
replica оператор перезапускает Core, и startup reconciliation повторно объявляет
active generation только replica с расхождением. Успешная начальная operation завершается
после ACK всех обязательных replicas; уже завершённая как `failed` попытка
остаётся в истории со своим исходом, даже если последующий reconcile устранил
drift.
Повтор не replay-ит пользовательский plugin Call с неизвестным исходом.

Core Management API `POST /api/plugins/{pluginId}/rollback` начинает новое
roll-forward на содержимое `previous`: Core атомарно меняет `active` и
`previous` местами до REST уведомлений. Plugin не получает
специальную rollback-команду: Core вызывает обычный `Reload` с generation,
ставшим active.
Подтвердившие rollback generation replicas остаются eligible; отставшие
fenced/degraded до startup reconciliation после ручного перезапуска Core.
Автоматической compensation назад нет.

## Roll-forward зарегистрированных replicas в v2

В v2 Core повторно сводит только актуальный desired generation с replicas,
имеющими живую аутентифицированную lease. При регистрации Core объявляет текущий
`active` только этой replica; периодическая reconciliation затем проверяет точные identity,
generation и digest через Plugin SDK readiness. Уже сошедшаяся replica не
получает лишний вызов. Отстающей или неготовой replica повторно объявляется тот
же immutable `Reload(generation, digest, schemaVersion)`, а результат
фиксируется отдельно по replica. Отказ одного участника не блокирует попытки к
остальным. Просроченная lease исключает replica из reconciliation и dispatch.

Повторяется только идемпотентное уведомление о desired generation. Core не
повторяет пользовательский plugin Call, submission, artifact upload или любой
вызов с неизвестным побочным результатом. Частичный rollout остаётся
roll-forward: нужные replicas fenced до точного ACK, а ручной rollback — новое
desired generation, а не автоматическая compensation. Периодичность
reconciliation задаётся versioned Core policy. Для instance, прошедшего
аутентифицированную регистрацию в текущем процессе Core, частично применённая
config operation остаётся `running`, пока точная incarnation остаётся живой
либо её сохранённая lease не истекла; recovery повторяет только идемпотентный
`Reload` для неподтвердивших targets. Истечение frozen lease или регистрация
другой incarnation с тем же `replicaId` атомарно завершает operation как
`failed/target_lost` и закрывает rollout. Желаемый `active` не откатывается,
новая incarnation не подставляется, а дальнейшее применение требует новой
явной операции. TypeScript
child-process fixture запускает реальный Core runtime process через target
bootstrap, регистрирует replica по
mTLS, проверяет directory poll, перезапускает Core и подтверждает, что
зарегистрированный instance требует повторной регистрации и не использует
static resolver fallback. Дополнительные SQLite/process fixtures проверяют
durable cohort, exact-incarnation ACK, фильтрацию восстановления и rollback.
Это локальные доказательства текущего WIP, не hosted release gate: обычная
сборка с закреплённым опубликованным SDK пока несовместима.

Открытыми частями v2 остаются release/config cohort rollout, совместимость
релизов, drain, внешний traffic-controller intent/confirmation, нагрузочная
проверка времени удержания activation lock и полный hosted/clean-environment
recovery gate. Потеря exact incarnation имеет terminal outcome
`failed/target_lost`; оно проверено локальным integration fixture, но ещё не
входит в hosted release gate.

Плагин удаляет отозванные revision-bound secret bytes из памяти после
подтверждённой смены конфигурации либо shutdown. Secret grants выдаются через
Plugin SDK REST только для раскрытия plugin-owned secret references. Их нельзя
использовать для разрешения межплагинных вызовов.

## Межплагинная авторизация

В Core v2 Core хранит peer policy, распространяет её и выдаёт
plugin-to-plugin interaction grants. Каждый plugin получает только разрешённую
выборку policy для своей replica; protocol library не знает продуктов или
именованных capability contracts.
Централизованные caller/target policies, generation/ACK и bounded drain
являются частью Core v2 SQLite и Management API.

Caller→target policy становится Core-owned durable desired state в
SQLite и редактируется только защищённым Management API. `GET/PUT/DELETE`
ресурса пары использует монотонную revision/ETag и CAS; успешная мутация
аудируется и публикует новый immutable in-memory snapshot. Bootstrap YAML не
содержит копию этих правил. Policy остаётся deny-by-default и содержит только
generic instance IDs, placement, carrier, weight и opaque contract ranges.
Plugin peer-directory long-poll пробуждается при смене policy или eligible
replica membership/readiness; lease expiry учитывается таймером ближайшего
deadline, без обязательного фонового reaper. SDK владеет wire-shape long-poll,
Core — авторизацией, durable policy и сборкой caller-scoped directory.

`pluginprotocol` обеспечивает только общий межплагинный обмен. Он не задаёт
plugin Manifest, REST lifecycle, settings, health API, secret redemption или
имена продуктовых методов. См. [границы protocol и SDK](protocol).

## Startup и crash recovery

До объявления plugin replicas готовыми Core:

1. открывает SQLite, применяет поддерживаемые миграции и проверяет integrity;
2. восстанавливает durable operations, `active`/`previous` и digest;
3. строит in-memory snapshot из committed desired state;
4. соединяется с каждым известным replica endpoint, сверяет identity и
   применённые generation, затем вызывает Reload и выдаёт точный generation при
   config pull;
5. продолжает незавершённые rollout вперёд; binding остаётся fenced, пока
   конкретная replica не подтвердит требуемую версию.

| Точка сбоя | Восстановление |
| --- | --- |
| До SQLite transaction | Авторитетными остаются прежние `active`/`previous`; Reload не отправлен. |
| После transaction, до Reload | Новый `active` и прежний `active` как `previous` уже durable; Core продолжает roll-forward после restart. |
| После promotion в active, до Reload | Core продолжает ту же durable operation и уведомляет replicas. |
| После частичных ACK | Roll-forward; подтверждённые остаются eligible на target, остальные fenced до startup reconciliation после ручного перезапуска Core. |
| После всех ACK, до operation completion | Core сверяет replica digest и завершает ту же operation; slot pointers уже committed. |
| После slot promotion, до публикации snapshot | Snapshot строится из committed SQLite state и не допускает stale replicas к target traffic. |
| Во время reconnect | Неизвестный Call не воспроизводится; затронутый stream закрывается, replica fenced до сверки. |

Повреждённая БД, digest mismatch или невозможность однозначно восстановить
generation блокирует только соответствующие instance/bindings и требует
операторского решения; Core не угадывает конфигурацию из runtime плагина.
Backup Core включает согласованный SQLite snapshot, создаваемый standalone
универсальным `liapoldus` CLI через target adapter; отдельно сохранённые Core bootstrap/configuration
artifacts. Restore выполняется только при остановленном Core; CLI проверяет
schema version, integrity и foreign keys и заменяет файл атомарно. Backup
plugin volumes выполняется отдельно и включает plugin-owned данные, например
Server site releases и ACME state.

Подробные публичные errors, operations и endpoints описаны в
[Management API](/spec/management.openapi.yaml), [error catalog](/spec/errors.json)
и [матрице приёмки](../configuration/acceptance).
