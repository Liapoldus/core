# TODO — Gateway Core

Единый roadmap и порядок реализации:
[Перепроектирование Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Здесь оставлены только незавершённые задачи Core; Constructor/react-lib не входят
в этот этап.

Нормативные детали: [целевая архитектура](https://liapoldus.github.io/gateway/architecture/target),
[SQLite и recovery](https://liapoldus.github.io/gateway/architecture/control-plane),
[plugin deployment](https://liapoldus.github.io/gateway/architecture/plugin-deployment).
Продуктовые плагины ведут собственные TODO; Core отслеживает только общий
plugin lifecycle и не перечисляет продукты или их возможности. TODO не
дублирует contracts из `pluginprotocol`.

## 1. Generic desired configuration в SQLite

- [ ] Завершить переход от текущей compatibility-модели `plugin_instances` к
  generic desired documents без plugin-specific парсинга; хранить logical
  instance отдельно от replica endpoint membership и durable operation state.
- [x] Хранить versioned settings revisions, schema version, digest,
  `current`/`previous`/`pending` pointers в SQLite и при инициализации
  backfill-ить активную revision из существующей записи plugin instance.
- [x] Реализовать application workflow candidate → typed applier → ACK →
  active pointer commit. Отклонённый applier-ом candidate помечается failed;
  конфликт ожидаемой current revision отклоняется.
- [ ] Подключить workflow к `PUT /api/plugins/{id}/settings`, Operation API и
  аудитируемому CAS из Management API. Application pipeline и supervised
  Runtime→`pluginprotocol.ConfigApply` с exact revision ACK реализованы; пока
  они проверены отдельно, без Management write endpoint и durable operation.
- [ ] Принимать только полную versioned JSON revision; сверять Manifest и
  `ConfigSchema`, capability→mode descriptors и release compatibility перед
  apply, не интерпретируя product fields.
- [ ] Применять ConfigApply ко всем обязательным replicas по индивидуальным
  endpoints; readiness ACK должен включать replica identity, revision и digest.
  Балансируемый Service ответом всех replicas не считается.
- [ ] Построить immutable in-memory snapshot из последнего durable generation.
  Ни Management mutation, ни process restart не должны приводить к чтению SQLite
  на пользовательском request path.
- [ ] Добавить schema validation, durable apply journal, exact replica ACK и
  crash-safe компенсацию всех participants. При ошибке предыдущая revision
  остаётся активной, а diagnostics не содержат конфигурацию или секреты.
- [ ] Добавить startup recovery для каждого crash point между candidate,
  protocol apply, ACK и SQLite commit. Core не открывает readiness при
  неизвестном/несогласованном active generation.
- [ ] Хранить SQL в source-owned `.sql` и embed-ить при сборке. Удалить inline
  SQL из Go и заменить внутренние ad-hoc errors на typed errors; публичные
  status/code/body оставить под versioned contracts.
- [ ] Реализовать SQLite backup/restore и consistency validation для БД,
  versioned Core package files и durable journals. Core SQLite использует только
  локальное persistent storage; PostgreSQL/S3 не вводить.

## 2. Generic plugin lifecycle

- [ ] Подключить generic plugin instance CRUD, settings CAS, endpoints,
  per-replica identity, limits, grants и audit по Management OpenAPI. Любой
  endpoint и capability остаются данными Manifest, не ветками Go кода.
- [ ] Завершить глобальный `supervised` profile: TUF trust bootstrap/rotation,
  signed release selection, package verification, safe extraction, immutable
  install revisions, process start/stop/restart, bounded backoff, resource
  limits и rollback.
- [ ] Завершить `external` profile: фиксированный endpoint на replica,
  credential providers, connection handshake/health, reconnect, readiness,
  drain и rollout. В этом профиле Core не устанавливает, запускает, останавливает
  и не перезапускает процессы.
- [ ] Для обоих profiles поддержать полный restart lifecycle: Bootstrap,
  Manifest, ConfigSchema, `ConfigApply`, `DispatchApply`, health/readiness; после
  Core restart заново передать последнее активное поколение. Не replay-ить Call
  с неизвестным исходом; оборванный Stream закрывать.
- [ ] Подключить готовый `pluginprotocol/sdk.LocalSession` в Core supervised
  lifecycle: protocol теперь передаёт listener и private bootstrap pipes,
  выполняет pinned local mTLS и health gate; Core должен использовать этот
  session для старта, наблюдения, остановки и restart/backoff процесса. Core не
  создаёт transport/TLS самостоятельно; plaintext/insecure local fallback
  запрещён. Application settings нельзя передавать через argv/environment или
  application config files.
- [ ] В `external` сохранять exact endpoint + expected identity каждой replica;
  реализовать membership CAS/update и не подключать Docker/Kubernetes API.
  Process rollout/restart/drain остаются ответственностью оператора.
- [ ] Подключить scoped config/call grants, secret reference resolution и
  redaction на реальном `serve` composition. Core не является CA; management и
  workload trust roots раздельны.

## 3. Plugin-to-plugin policy и поколения

- [ ] Хранить caller → target/capability/mode rules в SQLite с CAS, audit и
  deny-by-default. Core не проксирует capability payloads.
- [ ] Применять полный `DispatchApply` generation каждой нужной replica и
  проверять replica-bound ACK; candidate не становится active, пока все
  участники не подтвердят один generation/digest.
- [ ] Обеспечить atomic peer-client view после ACK: только объявленные target,
  capability/mode, endpoint и identity; deny-by-default и отсутствие Core
  payload proxy.
- [ ] Доказать reconnect, rollout/drain и revocation behavior с удалённым
  plugin: отдельно control/data identities, внешняя PEM/SPIFFE provisioning,
  подписанный CRL и отсутствие plaintext downgrade.
- [ ] При отказе одной replica деградировать только зависимые capabilities;
  остальные plugins и control plane остаются работоспособными.

## 4. Plugin integration and conformance

- [ ] Подключить generic Admin Surface lifecycle к Management API: enumerate
  объявленные surfaces, авторизовать вызов по instance scope, передавать
  версионированные JSON payloads через protocol и аудитировать результат.
- [ ] Обеспечить, чтобы product settings, runtime state, artifacts и их
  capabilities принадлежали подключённому plugin; Core хранит только generic
  desired settings, endpoints, grants, revisions и audit metadata.
- [ ] Проверить подключённый plugin на реальном child process: ConfigSchema,
  ConfigApply, health, shutdown, restart/recovery и объявленные invocation
  modes. Product-specific traffic и persistent-state conformance ведутся в
  соответствующем plugin repository.

## 5. API, access, audit и CLI

- [ ] Завершить durable Management operations, idempotency, service-key
  rotation/revocation, audit всех mutations и безопасную operation recovery.
- [ ] Добавить профильно-ограниченные Management API/CLI: install/lifecycle
  доступны только в `supervised`; `external` предоставляет только endpoint,
  desired-state и health operations.
- [ ] Не вводить в Core plugin-specific site/certificate routes, runtime
  pass-through или отдельную route DSL; такие операции доступны только через
  generic Admin Surface, объявленный соответствующим plugin.
- [ ] Описать instance lifecycle и привилегии в OpenAPI: supervised install /
  process actions доступны только в этом profile; external допускает desired
  endpoint/settings/policy/health operations, но не process management.
- [ ] Проверить web Controller private HTTPS+mTLS и binding-specific Bearer,
  desktop ограниченный SSH port-forward, authorization per request, audit и
  полную redaction. Constructor и `react-lib` не менять до завершения Gateway v1.

## 6. Обязательные conformance gates

- [ ] TS red/green suites под `tests/` для config generations/recovery, TUF
  install/rollback, local/remote plugin lifecycle, per-replica
  `ConfigApply`/`DispatchApply`, direct interactions, mTLS/CRL, cookies, Admin
  Surface, security и audit. Product-specific data-plane conformance lives with
  its plugin.
- [ ] Исполняемые public golden vectors, `make check`, `go vet ./...`,
  `make staticcheck-u1000`, `go build ./...`, macOS/Linux builds и Docker smoke
  пройти на затронутых компонентах.
- [ ] Не объявлять v1 готовым до полного cross-component recovery,
  protocol/security, generic lifecycle и подключённых plugins conformance gates.

## Инструкция workspace, требующая синхронизации

- [x] Корневой `../AGENTS.md` синхронизирован с целевым ownership: Core —
  singleton control plane; `supervised` и `external` являются
  взаимоисключающими global profiles. Дата синхронизации — 2026-09-28.
