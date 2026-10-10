# План v2/v3

> Канонический целевой контракт production-ready v3 находится в
> [целевой архитектуре v3](v3). Эта страница сохраняет только историческую
> последовательность работ и не может переопределять ownership или API v3.

Страница фиксирует согласованный следующий этап и не является альтернативным
репозиторным TODO. Текущие owner tasks и проверяемые статусы находятся в
[Core TODO](https://github.com/Liapoldus/core/blob/main/TODO.md) и TODO владельцев
SDK, protocol и plugins. Исторические этапы v1 не перечисляются как незакрытый
backlog.

## Текущие архитектурные границы

- Core — plugin-agnostic control plane и единственный writer своей SQLite.
- Bootstrap-параметры поступают из ENV; Core сам один раз создаёт SQLite и
  initial settings revision до открытия Management API. Рабочие настройки Core
  и plugins изменяются через защищённый versioned API.
- Configurations plugins — точные plugin-owned JSON bytes. Core валидирует
  envelope, digest/CAS, durable operations и generation lifecycle, но не
  интерпретирует product fields.
- Plugin SDK — независимый Core↔plugin REST contract. `pluginprotocol` — общий
  plugin↔plugin transport contract; это разные репозитории и границы.
- Plugin replicas сами регистрируются с mTLS identity, получают leases и
  fenced при истечении lease, конфликте incarnation или отзыве credentials.
- Core не запускает процессы, не управляет контейнерами и не выбирает deployment
  provider. Deployment automation — standalone `liapoldus` CLI и его adapters.
- Core не содержит пользовательского CLI. Project/Git resolution, bundle
  materialization, target selection, approval checks, local Core lifecycle и
  GitHub CI принадлежат [`Liapoldus/cli`](https://github.com/Liapoldus/cli).
- Studio работает только с Project/Git и импортированными CLI reports; она не
  содержит Core API adapter или Core credentials.
- Deployment profiles остаются внешней ответственностью. Ни описания, ни
  шаблоны не означают поддержку без воспроизводимого native smoke.
- Studio и CLI являются разными продуктами: Studio готовит commit-backed source,
  CLI выполняет plan/apply к одному или нескольким Core targets.

## Критерии реализации

v2 включает завершение API-driven deployment/rollout через standalone CLI,
одноразовый runtime bootstrap и cross-repository bundle contract; профили standalone,
Docker, Swarm и Kubernetes только после smoke для каждого, traffic-weighted
seamless rollout только при наличии traffic controller, а также согласованные
Core/SDK/protocol/Domain/Runtime проверки. Полный v2 gate начинается ниже;
незакрытые owner checks остаются блокирующими и не считаются выполненными по
наличию шаблонов или документации.

## Production readiness v2 — межсубъектная приёмка

v2 считается готовой только как согласованный релиз экосистемы, а не по
отдельным зелёным сборкам. Контракты и доказательства принадлежат владельцам;
эта секция задаёт общие сквозные критерии и не дублирует product schemas.

- **CLI boundary:** Core starts without a user-facing command dispatcher,
  performs one-time bootstrap from CLI-provided references, accepts only canonical
  bundle requests with immutable commit SHA/digest, and never reads Git or CLI
  state. `liapoldus` local, remote and GitHub CI workflows use the same API
  contract.
- **Project-to-SQLite path:** CLI resolves a Git commit, validates and normalizes
  project files, sends a bundle through Management API, and Core writes bundle
  provenance plus desired configuration in one SQLite transaction. CLI never
  opens Core SQLite; Studio never calls Core API.
- **Approval and reproducibility:** apply is rejected without exact commit,
  required remote approval, compatible schema/API, idempotency key and CAS
  evidence. The evidence chain is commit → bundle digest → operation →
  generation → replica acknowledgements.
- **Replica lifecycle:** Core переживает рестарт с пустой in-memory directory;
  новые incarnation регистрируются с уникальной mTLS identity, leases истекают
  по TTL, stale/подменённая replica fenced, а peer-directory не создаёт новую
  product config generation.
- **Rollout:** release digest/config generation, совместимость когорт,
  Reload/ACK, drain и внешние traffic weights работают как единая операция.
  Partial ACK и отказ traffic controller останавливают продвижение на последнем
  подтверждённом состоянии; неизвестный результат не replay-ится. Для
  неподтверждённой или несовместимой когорты нет нового трафика.
- **Placement и carriers:** вручную либо внешним Docker/Swarm/Kubernetes
  operator-ом запущенные workloads регистрируются без Core orchestration API.
  TCP/QUIC/Unix socket/Windows named pipe выбираются явно; mTLS, identity,
  revocation и отсутствие fallback проверены на каждой заявленной платформе.
- **Peer-link policy:** правила caller instance → target instance создаются и
  изменяются только через Core Management API, хранятся durable в SQLite с
  CAS/ETag и audit, а эффективная политика публикуется через SDK-defined
  long-poll directory. Проверены policy update/remove, deny-by-default,
  caller-scoping, directory wake-up и немедленное исключение replica после
  lease expiry без отдельного фонового reaper.
- **Server и forms-db:** их текущая реализация остаётся regression baseline;
  Server-specific scaling/storage/ACME conformance и forms-db multi-replica
  SQL/cohort compatibility перенесены в v3. В v2 Core/SDK rollout и product
  compatibility проверяются на нейтральных fixtures; эти product repositories
  не изменяются.
- **Domain и Runtime:** минимум три Domain voting nodes проходят leader failover,
  quorum loss, fresh-read barriers, bounded snapshots и согласованную model
  migration/rollback. Runtime проходит WASM sandbox/limits, artifact integrity,
  compatibility rollout и Domain authorization/fencing без cross-plugin ACID
  обещаний. Runtime-side HTTP terminal adapter использует только неизменённый
  опубликованный Server contract; изменение Server repository или переход на новую Server
  API входит в v3.
- **Сквозной и эксплуатационный gate:** clean-environment сценарий запускает один
  Core и отдельно размещённые плагины; проверяет restart/reconnect, конфигурацию,
  прямые разрешённые peer-вызовы, mTLS/CRL rotation, redaction, recovery,
  generic rollout, Domain failover, Runtime peer/WASM и отсутствие payload в
  Core. Каждый заявленный OS/carrier и Domain storage profile исполняет native gate;
  cross-build или schema-only test не заменяет runtime evidence.
- **Согласованный релиз:** все owner suites, Core/SDK/protocol gates, VitePress,
  cross-repository E2E и deployment walkthrough имеют воспроизводимый PASS;
  owner TODO содержит точные команды/окружения и открытых blockers нет. До
  прохождения последнего пункта система не называется production-ready v2.

Если для конкретного deployment или filesystem profile нет среды, это остаётся
непройденным gate с названием отсутствующего профиля/доказательства, а не
условным PASS. Полный план лежит в workspace `tasks/`, а BigPickle dispatch
prompts — в `bigpikle/`; эти каталоги не публикуются VitePress и не заменяют
нормативные owner TODO.

## Будущие этапы — после Core v2

Не создавать дополнительные Core API, SQLite state, dependencies или acceptance
под функции будущих этапов. Core v2 охватывает self-registration/rollout, внешнее размещение,
смешанные peer transports, Domain и Runtime. Server/forms-db repositories не
меняются в v2; их текущие paths — только regression targets. Studio и standalone
CLI входят в текущий v2 межрепозиторный workflow. Caddy-L4/public TCP/UDP, CAPTCHA/Identity,
Core embedding API, Plugin SDK in-process/static composition, все дальнейшие
Server/forms-db product changes (включая Server
scaling/shared storage и forms-db multi-replica SQL compatibility/website content),
отложены до v3. Установку и плановые обновления Core/plugins выполняет оператор
выбранными средствами; Core не получает provider
API ни в одном этапе. Identity и CAPTCHA заморожены до
отдельной явной разморозки. `pluginprotocol` остаётся
единственной Go wire/session реализацией; второй Python engine не создаётся.
Foreign bindings в v3 не заявляются. Core и SDK composition описаны в
[целевой архитектуре](target) и [целевой архитектуре v3](v3).

### V3: Plugin SDK in-process adapter и единый бинарник

В v3 реализован in-process adapter для статически включённых доверенных Go
plugins. Он заменяет только Core↔plugin REST внутри одного процесса: Core
вызывает generic `Reload` interface, plugin сам pull-ит точное поколение через
scoped `ConfigSource`, реализованный над immutable Core snapshot. Семантика
generation/digest/ACK, rollback, grants, authorization, audit и errors остаётся
общей с REST adapter. В том же SDK сохранить REST+mTLS adapter для отдельных и
удалённых plugin processes; выбирать adapter явно на instance/composition, без
fallback. Один executable с дочерними plugin processes остаётся REST-режимом,
не in-process.

In-process компоненты — одна граница доверия и отказа; mTLS между ними
неприменим, panic containment не даёт process isolation. Поэтому в этот режим
включаются только доверенные compiled-in modules. `pluginprotocol` не меняется:
межплагинные вызовы остаются на его generic API с mTLS; прямые Go-вызовы и
каналы между plugins не допускаются.

**Gate:** SDK lifecycle conformance проходит одинаково через REST и in-process;
проверены pull exact generation, exact raw bytes/digest, ACK/retry/failure,
instance scoping, authorization/grants/audit и отмена. Smoke доказывает, что
in-process профиль не открывает Core↔plugin REST listener, REST-профиль
сохраняет mTLS, а plugin-to-plugin mTLS не изменяется.

### V3: Core как Go library и статическая композиция

Core предоставляет публичный host API, позволяющий Go-приложению создать,
запустить, проверить readiness и остановить один Core runtime. API использует
штатную composition root и bootstrap/config source; не раскрывает `internal/`
типы, не обходит SQLite и не создаёт параллельную модель конфигурации. Core
остаётся singleton относительно своей state database.

Отдельный сборочный composition root включает Core и выбранные plugin factories
в один executable. Встроенные Go plugins подключаются через Plugin SDK
in-process adapter; REST adapter остаётся для отдельно запущенных и удалённых
plugins. Состав фиксируется на build time; dynamic Go plugin loading,
независимое обновление встроенного plugin без пересборки и обещание process
isolation не входят в scope. Упаковка executable с child processes сама по себе
не переводит их на in-process transport.

**Gate:** host API smoke создаёт/закрывает один Core и освобождает ресурсы;
composed binary содержит только явно включённые factories; in-process plugin
проходит общий SDK Reload/config-pull/ACK corpus без REST listener. Отдельный
process сохраняет REST+mTLS и тот же lifecycle contract. Ни один plugin не
обходит Core/peer security boundary прямым вызовом `pluginprotocol`.

### V3: Server и forms-db product development

В v3 возобновить работу в `plugins/server/` и `plugins/forms-db/`; до этого их
  текущие behavior/contracts не менять, кроме отдельно одобренного исправления
  критического дефекта. Server scope объединяет multi-replica registration,
release compatibility, shared site/CertMagic storage и ACME conformance с
Caddy-L4/public TCP/UDP отдельными gates. forms-db scope объединяет replica и
SQL schema compatibility/rollout для PostgreSQL, MySQL и MariaDB с website/
content evolution. Core остаётся product-agnostic; owner contracts принадлежат
plugins, а общие v2 lifecycle gates покрываются fixtures.

### V3: forms-db website и управление контентом

В v3 отдельно спроектировать развитие forms-db в website/content plugin:
контент принадлежит forms-db storage, административное редактирование доступно
через plugin-owned UI, а endpoint/порт панели отделён от публичной доставки
сайта. Публикацию и HTTP listener необходимо согласовать с Server plugin; Core
хранит только opaque plugin settings и остаётся product-agnostic. Эта задача не
меняет опубликованные contracts и не предполагает, что текущие права `platform-admin`
автоматически являются пользовательской ролевой моделью сайта.

Порядок: (1) выбрать ownership между одним расширенным forms-db и отдельным
website plugin; (2) согласовать модель контента, schema evolution и DB
migrations; (3) определить, кто обслуживает публичный сайт и кто владеет
отдельным admin listener-ом; (4) спроектировать authN/authZ, provisioning,
roles, audit и network exposure; (5) определить публикацию, immutable release,
`current`/`previous`, rollback и восстановление; (6) обновить owner contracts,
security model и conformance; (7) только затем реализовывать сквозной
Core → Plugin SDK → forms-db/Server сценарий.

Канонические продуктовые вопросы и задачи принадлежат
[forms-db v3 TODO](https://github.com/Liapoldus/forms-db/blob/main/TODO.md) и
[документации forms-db](https://liapoldus.github.io/plugins/forms-db#направление-развития-в-v3-website-и-редактор-содержимого).
Этот roadmap задаёт лишь межсервисную последовательность; он не копирует
forms-db schemas или product API. Ни website/content, ни replica/SQL
compatibility forms-db не входят в v2 agent prompt.

## Правила агентной работы

Агенты могут выполнять независимые вертикальные slices параллельно, но у каждого
среза один файловый/контрактный owner. Разрешено удалять старую архитектуру,
публичные legacy surfaces и код без compatibility shim, если миграционный путь
и владельцы проверены, а replacement contract/tests готовы. Не удалять данные,
чужие dirty/untracked изменения, архивы или репозитории. Изменения проверяются
по AGENTS.md целевого репозитория; вся реализация тестируется по целевому
поведению, не по старой реализации.
