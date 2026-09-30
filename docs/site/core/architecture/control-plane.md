# Control plane: SQLite, конфигурации и поколения

Эта страница описывает долговременное состояние Core и доставку конфигурации
плагинам. Core всегда один; SQLite — единственная долговременная база Core и
источник desired configuration. Каноническое распределение ответственности
задано в [целевой архитектуре](target), а общий REST plugin contract принадлежит
отдельной библиотеке Plugin SDK. `pluginprotocol` в этой модели не участвует в
управлении Core и используется только для plugin-to-plugin взаимодействия.

## Состояние Core в v1

SQLite хранит только control-plane state, нужный для восстановления и объяснения
операций:

| Данные | Содержимое | Не хранить |
| --- | --- | --- |
| Plugin instance | Стабильный ID, plugin identity, зарегистрированные replicas и timestamps. | Product-specific поля, binaries и process lifecycle. |
| Replica membership | Стабильный ID replica, отдельный REST control endpoint, ожидаемая mTLS identity и desired/observed status. | Private keys, bearer tokens и TLS secret bytes. |
| Config generations | До одной строки на instance/slot: `instance_id`, монотонный `generation`, `slot`, точный `raw_json BLOB`, `sha256`, `schema_version`, `created_at`. Слоты: `active`, `previous` и непубликуемый `staging`. | Product-specific распарсенные поля и раскрытые секреты. |
| Replica acknowledgements | Config generation, digest, replica identity и время подтверждения. | Credentials целиком. |
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
  и plugin-to-plugin grants не входят в v1.
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
   завершает operation. До этого operation остаётся `degraded`/`rolling_forward`,
   а новый immutable snapshot разрешает traffic только через replicas,
   подтвердившие именно active generation.

`Reload` идемпотентен по instance, replica и generation. Повторный config pull
`GET` возвращает тот же неизменяемый документ; Core не подменяет содержимое уже
выданного generation. Плагин не опрашивает Core постоянно: он получает
инициирующий Reload, а конфигурацию забирает сам. При запуске/переподключении
плагин сообщает своё применённое поколение; Core сравнивает его с desired state
и инициирует Reload при расхождении.

## Частичный rollout и rollback

Принята стратегия **roll-forward**. После продвижения candidate `active` не
возвращается автоматически к старой версии из-за частичного отказа. Подтвердившие
replicas обслуживают новый active generation; отставшие исключены из зависимого
traffic/peer calls, instance отмечен degraded, а Core повторяет `Reload` только
для отставших. Operation завершается после ACK всех обязательных replicas.
Повтор не replay-ит пользовательский plugin Call с неизвестным исходом.

Core Management API `POST /api/plugins/{pluginId}/rollback` начинает новое
roll-forward на содержимое `previous`: Core атомарно меняет `active` и
`previous` местами до REST уведомлений. Plugin не получает
специальную rollback-команду: Core вызывает обычный `Reload` с generation,
ставшим active.
Подтвердившие rollback generation replicas остаются eligible; отставшие
fenced/degraded и получают retry. Автоматической compensation назад нет.

Плагин удаляет отозванные revision-bound secret bytes из памяти после
подтверждённой смены конфигурации либо shutdown. Secret grants выдаются через
Plugin SDK REST только для раскрытия plugin-owned secret references. Их нельзя
использовать для разрешения межплагинных вызовов.

## Межплагинная авторизация: граница v2

В v1 Core не хранит peer policy, не распространяет её и не выдаёт
plugin-to-plugin interaction grants. Каждый вызывающий plugin отвечает за свою
authorization policy; protocol library получает только generic consumer-supplied
authorizer и не знает продуктов или именованных capability contracts.
Централизованные caller/target policies, generation/ACK и bounded drain
отложены до v2 и не являются частью Core v1 SQLite или Management API.

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
| После частичных ACK | Roll-forward; подтверждённые остаются eligible на target, остальные повторно получают Reload и fenced. |
| После всех ACK, до operation completion | Core сверяет replica digest и завершает ту же operation; slot pointers уже committed. |
| После slot promotion, до публикации snapshot | Snapshot строится из committed SQLite state и не допускает stale replicas к target traffic. |
| Во время reconnect | Неизвестный Call не воспроизводится; затронутый stream закрывается, replica fenced до сверки. |

Повреждённая БД, digest mismatch или невозможность однозначно восстановить
generation блокирует только соответствующие instance/bindings и требует
операторского решения; Core не угадывает конфигурацию из runtime плагина.
Backup Core включает согласованную SQLite backup и Core configuration
artifacts. Backup plugin volumes выполняется отдельно и включает plugin-owned
данные, например Caddy site releases и ACME state.

Подробные публичные errors, operations и endpoints описаны в
[Management API](/spec/management.openapi.yaml), [error catalog](/spec/errors.json)
и [матрице приёмки](../configuration/acceptance).
