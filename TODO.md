# TODO — Gateway core

Единый архитектурный roadmap и очередность работ:
[Перепроектирование Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Здесь перечислены только незавершённые задачи core.

## Актуальный прогресс — 25.09.2026

- Production `serve` использует bootstrap + SQLite, а Management API защищён
  SQLite-backed Bearer verifier. Пути state/artifacts разрешаются относительно
  `gateway.yaml`; пустой Gateway запускается без public data plane.
- `/api/groups` и `/api/groups/{id}` читают SQLite. `POST /api/groups` создаёт
  application group, проверяет опубликованные ID/idempotency constraints,
  возвращает `201`, `400 invalid_request` или `409 group_already_exists`.
- По разрешённому cleanup удалены старый `internal/infrastructure/network`,
  CompiledGraph/config DSL compiler и renderer, site/release registry и snapshot
  stores, их CLI/account store, GeoIP/MMDB runtime и telemetry exporters.
  Удалены неиспользуемые route/proxy/WAF/Geo/telemetry domain модели и старые
  route/policy/resolver ports. Management contract больше не публикует config,
  site, listener/upstream или metrics endpoints. Удалены привязанные к ним
  красные legacy suites и фикстуры; групповой SQLite,
  минимальный bootstrap, Caddy runtime и plugin fixtures сохранены.
- Bootstrap-проверки оставлены и обновлены: TS integration проверяет загрузку
  относительных путей, запрет прежнего route DSL и отказ удалённому bind без
  client CA. `bearerVerifier` удалён из core fixtures/tests.
- Убраны недействующие CLI-конфигурационные и `site` команды; CLI оставлен для
  `serve` и `access bootstrap`. Статический CLI contract сокращён до реально
  используемых слов и настроек bootstrap key.
- Удалены оставшиеся не маршрутизируемые `/api/sites` publish/rollback
  handlers, их volatile idempotency/revision-conflict обвязка и domain error;
  добавлен TS architecture gate, не допускающий возврат старого registry API.
- Удаление старых metrics/tracing exporters не закрывает целевую observability:
  документационные требования к Gateway/Caddy/plugin readiness, access и
  application logs, metrics, traces и общей redaction policy остаются в v1
  roadmap и должны быть реализованы заново на новых runtime adapters.
- Последняя проверка после cleanup: Vitest 25 files / 43 tests, architecture
  lint без предупреждений, `go vet ./...`, `go build ./...` и
  `git diff --check` прошли. Это не означает готовность Gateway v1.

## Документальный контракт и тестовый фундамент

- [x] Удалить недоступные legacy config/site/release handlers и связанные с
  ними неиспользуемые модели/контрактные поля. Целевые group release, plugin,
  TLS и Caddy Admin surfaces остаются незавершёнными задачами ниже.
- [ ] Создать отдельные TypeScript unit/integration/E2E suites под tests для
  bootstrap rejection, SQLite, group multipart, native Caddyfile adaptation,
  Caddy Admin checkpoint/drift/reconcile, direct Caddy-to-plugin dispatch и
  recovery.
- [ ] Добавить TS red/green coverage до расширения целевого data/control-plane:
  multipart group publish/rollback, Caddy adapt/load and atomic snapshot,
  external Caddy process, plugin replica readiness/DispatchApply, cookies,
  Admin API checkpoint/reconcile и crash recovery. Legacy runtime suites
  удалены, а не объявлены эквивалентом этих ещё не написанных проверок.

## Bootstrap, persistence и Management API

- [ ] Завершить Management API semantics и storage для операций, audit,
  idempotency и Caddy checkpoints; текущий bootstrap slice покрывает только
  service-key verifier и группы.
- [ ] Реализовать строго минимальный gateway.yaml: state SQLite path, immutable
  artifacts root, Management bind/TLS/trust и embedded/external Caddy variant.
- [ ] Разработать SQLite migrations/repositories для groups/revisions/current/
  previous, plugin instances, service-key verifier metadata, operations,
  idempotency, audit, checkpoints и retention.
- [ ] Закрепить foreign keys, WAL, transaction isolation, локальный storage,
  backup/restore и crash recovery; SQLite хранит durable metadata, а
  Caddyfile/plugin settings revisions и большие artifacts остаются immutable
  files. На старте строить immutable in-memory RuntimeSnapshot; request path не
  читает SQLite/config files. Запретить dangling revision/artifact refs.
- [ ] Реализовать Management API security: private-network web backend доступ
  по mTLS + per-Gateway Bearer `platform-admin`; loopback/SSH-forwarded access
  по Bearer с TLS server verification. Gateway не реализует Constructor users
  или RBAC. Обеспечить one-time token reveal/rotation/revocation и redaction.
- [ ] Обновить CLI: bootstrap status/migrations, service-key lifecycle,
  group inspect/rollback, Caddy build identity, drift/checkpoint/restore и
  reconcile.

## Caddy variants, groups и Admin API

- [ ] Собрать embedded Caddy и запускать compatible external custom Caddy binary
  как обязательные v1 variants; stock Caddy недопустим.
- [ ] Зафиксировать Caddy/xcaddy/module versions, build identity/module
  manifest, supply-chain verification и общий parity matrix.
- [ ] Встроить обязательный Caddy-L4 в оба variants и пройти TCP/UDP conformance
  для macOS/Linux; провал блокирует v1, Go net/gnet fallback не разрешать.
- [ ] Реализовать native Caddyfile validation/adaptation, system group и
  стабильную composition application groups без Gateway route DSL.
- [ ] Реализовать один multipart group release: metadata CAS/idempotency,
  Caddyfile fragment, optional single safe tar.gz, frontends roots, digest,
  staging limits, durable artifact commit, full snapshot prepare/activate и
  rollback current/previous.
- [ ] Реализовать startup reconciliation незавершённых операций; crash между
  artifact/SQLite commit/Caddy activation не должен создавать смешанный runtime.
- [ ] Реализовать полный Admin API pass-through к loopback/local IPC; checkpoint
  до каждой mutation, drift detection, group publish block, explicit checkpoint
  restore и full-composition reconcile с If-Match.
- [ ] Не обещать обратную генерацию Caddyfile из произвольного native Caddy JSON.

## Plugins, TLS и Constructor boundary

- В рабочем дереве есть ограниченный, пока не подключённый к `serve` Caddy
  `call` slice: `StartCaddyfileWithPlugins`, per-config handshake и
  `liapoldus_plugin <instance> <capability> call`. Он не завершает plugin
  runtime v1: cookie actions пока fail closed, startup composition и snapshot
  generations не подключены, remote-replica fan-out не реализован. Сохранить
  этот код при подключении composition root и расширять по red TS тестам.
- [ ] Оставить core plugin-agnostic: CRUD generic instances/capability
  manifests/modes, local supervision и remote fixed Service endpoint без
  конкретных plugin names; режим задаётся per instance, mixed deployments
  разрешены; Caddy handler напрямую вызывает объявленную capability.
- [ ] Развести Gateway control gRPC client и Caddy data-plane connection pool:
  контроль выполняет Manifest/Config/health/lifecycle, Caddy напрямую вызывает
  только Call/Stream. Для remote выдать раздельные scoped workload identities;
  activation generation ждать readiness обоих каналов, передавать Caddy только
  immutable endpoint/capability/limit и credential references, не handles или
  plaintext ключи.
- [ ] Включить remote mTLS с уникальной externally-issued identity на каждую
  workload replica и привязкой к logical instance. Проверять protocol handshake
  на каждом новом connection и agreement по protocol version,
  release/Manifest/settings digests у всех Ready replicas; Management/plugin
  trust roots разделить, rotation/revocation
  выполнять без downgrade. `DispatchApply` уже реализован в `pluginprotocol` v1;
  Gateway обязан применить candidate generation к каждой Ready replica и
  получить/проверить индивидуальный acknowledgement до activation. Обычный
  load-balanced Service не даёт адресной доставки и подтверждения от каждой
  replica, поэтому сам по себе не подходит. Механизм discovery/индивидуальной
  адресации replicas ожидает решения пользователя; до решения не выбирать его
  молча и не начинать реализацию Gateway discovery.
- [ ] Реализовать bounded local restart/reconnect и remote Service reconnect;
  не повторять unary Call с неопределённым исходом, закрывать in-flight Streams,
  а недоступность одного plugin отражать только на связанных bindings.
- [ ] Сохранить Gateway-owned scoped GrantBroker и отсутствие direct
  plugin-to-plugin traffic. Пользовательский capability traffic идёт напрямую
  от Caddy handler к plugin, не через Gateway Management API или application
  dispatcher; GrantBroker обслуживает только отдельное redemption.
- [ ] Оставить Caddy/CertMagic единственным ACME owner; добавить readiness по
  домену и domain renew/revoke adapter/API без второго ACME state machine.
- [ ] Поддержать Constructor только через Management REST API; Caddy Admin port
  не публиковать, credentials не возвращать React renderer.

## Обязательные gates

- [ ] Для каждого increment сначала отдельные красные TypeScript tests, затем
  реализация; Go test files в production packages не добавлять.
- [ ] Для milestones запускать make check, go vet ./..., go build ./...,
  macOS/Linux builds и подходящие Docker/Caddy variant smoke suites.
- [ ] Добавить immutable dispatch generations и external-Caddy private Admin
  API/IPC synchronization; failure сохраняет прежний runtime generation.
- [ ] Проверять все Caddyfile binding `instance/capability/mode` по
  capability→modes descriptor из `pluginprotocol` Manifest до активации
  revision; частичная проверка только `call` сейчас есть в uncommitted Caddy
  adapter slice и должна быть подключена к реальной публикации/активации.
- [ ] Сделать golden vectors исполняемыми conformance-сценариями: текущая
  architecture-проверка подтверждает только структуру и checksum списка, но не
  поведение Gateway.
- [ ] Подключить прямой Caddy gRPC `Call`/`Stream` boundary, включая HTTP bidi,
  WebSocket, SSE и L4; application/control plane не буферизует пользовательский
  body.
- [ ] Не объявлять v1 готовым без полного cross-variant HTTP/TLS/ACME/L4/plugin/
  SQLite/recovery/security/admin-proxy conformance.
