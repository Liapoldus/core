# Текущая работа Core

## Подготовка перед v2

- [ ] V2 bootstrap cutover: Core сам однократно создаёт SQLite settings из
  `CORE_INIT_*` при первом запуске до открытия Management API; `core init` и
  `core serve` удаляются, а запуском Core управляет standalone `liapoldus` CLI.
- [x] Settings Core хранятся в SQLite с CAS revisions, audit, desired/effective
  состоянием, pending restart и offline recovery к прежней revision.
- [x] SQL, storage schema/migrations и постоянные диагностические определения
  принадлежат Go-коду; независимый Go build/test и строгие lint-конфиги добавлены.
- [x] SDK/API contracts переведены на закреплённые независимые module versions;
  агрегатор документов использует pinned YAML source manifest.
- [x] Удалён static certificate resolver fallback. Membership для динамических
  replicas определяется authenticated registration и активными leases.
- [x] Offline migration в SQLite: `core-migrate --dry-run` валидирует typed
  план, `--apply` берёт exclusive lock, сохраняет integrity-checked backup,
  импортирует Core settings и автоматически восстанавливает DB при ошибке.
  Plugin settings bytes/generations, audit и остальные таблицы не декодируются
  и проверены тестом на сохранение; static plugin IDs явно перечисляются как
  требующие повторной authenticated registration.
- [x] Удалить runtime bootstrap-loader fixture и перевести legacy YAML smoke на
  `core-migrate --dry-run`. Runtime child-process сценарии используют
  `CORE_SQLITE_PATH`, bootstrap settings API и standalone CLI; оставшийся YAML golden-vector
  проверяет только validator миграционного формата. Lifecycle, backup/restore
  и Core→plugin E2E покрыты отдельно.
- [ ] Завершить code-owned generation публичных schema/OpenAPI/error contracts
  и воспроизводимые проверки их публикации.
- [ ] Включить полный blocking quality gate на чистой ветке: Go, race, vet,
  staticcheck, architecture lint, TS tests/typecheck/lint и docs build. Go
  tests, Core Vitest (98 файлов, 168 тестов), TS strict typecheck, staticcheck
  U1000, typed ESLint (recommended + unsafe call/argument + async-safety rules)
  и architecture lint локально прошли. Отдельный audit unsafe
  assignment/member выявил 103 fixture JSON-boundary случая; добавлен object
  parser и закрыты 36 без подавлений. Остальные, golangci-lint и clean-branch
  CI прогон ещё не завершены.

## V2 — приоритетный этап

- [ ] Удалить Core CLI радикально одним breaking slice: `cmd/core` command
  dispatcher, `internal/presentation/cli`, `cmd/core-migrate`, CLI
  contracts/tests и CLI docs.
- [ ] Согласовать и опубликовать Core Management API contract для canonical
  config bundle: commit SHA, bundle digest, schema version, target metadata,
  idempotency, CAS, plan/apply и operation observation.
- [ ] Реализовать cross-repository contract tests с `Liapoldus/cli`; Core не
  импортирует CLI и не открывает SQLite через CLI.
- [ ] Перевести local/remote/multi-target deployment на standalone CLI и
  GitHub CI. Core принимает только API requests; local process bootstrap и
  lifecycle выполняет CLI/adapter.
- [ ] Обновить backup/restore/observability runbooks на `liapoldus core`
  commands и Management API; удалить `/core/cli/` как поддерживаемую surface.
- Поддержать standalone Linux, Docker, Swarm и Kubernetes только после native
  smoke каждого профиля. Наличие deployment manifests само по себе не означает
  поддержку.
- Изменение конфигурации и управление rollout выполняются через Core API,
  вызываемый standalone CLI из immutable Git revision.
- Гарантировать seamless candidate rollout только на профилях с traffic
  controller, поддерживающим веса. Для standalone такую гарантию не заявлять.
- Core остаётся независимым от Ansible и не содержит управления его playbooks,
  установкой или процессами.

## V3 — следующий этап

- Продолжить инфраструктуру Ansible в отдельном репозитории; продуктовые
  репозитории и Studio не зависят от него.
- Развивать Studio как отдельную среду проектов, файлов, Git и Studio plugins.
  Studio не имеет Core API adapter; target connections, deploy и Core
  lifecycle принадлежат standalone CLI/CI. Plugin configuration UI поставляет
  plugin; Studio встраивает только declarative surfaces.
- Отдельно описать и согласовать конкретные v3 API/contracts до реализации.

## Проверки последнего прохода

- `PATH=<bundled-node>/bin:$PATH GOWORK=off GOFLAGS=-p=1 make test` — прошёл
  целиком: Go tests, полный
  Vitest, TypeScript typecheck и blocking ESLint.
- `go test ./cmd/core-migrate -v` — прошли dry-run mapping, preserve-bytes,
  backup и transactional rollback tests нового offline import.
- Полный Core Vitest — 98 файлов прошли, 1 штатно skipped; 168/168 тестов
  прошли, включая ручной Core→Server→forms-db lifecycle и Admin Actions.
- `go vet ./...`, `make staticcheck-u1000`, `make arch-lint`, `git diff --check`
  — прошли.
  Архитектурный gate учитывает SQLite driver в settings adapter tests.
- `npm audit` в `tests/` — 0 vulnerabilities; Node type definitions закреплены,
  TypeScript typecheck добавлен в `make test`.
- `publish-contracts.mjs` и `verify-contract-bundle.mjs contracts/v1` прошли.
- VitePress aggregator build прошёл после исправления двух битых Core links.
  Не обновлять immutable source pins для незакоммиченных owner changes.
- Ручной Core→Server/forms-db прогон подтвердил restart, registration leases,
  settings rollback, plugin Admin query/delete, trust-root rotation и отказ
  от отозванной identity.
- Server Go tests и полный Vitest suite — прошли; forms-db Vitest — 22 файла,
  35 tests passed, 6 skipped; отдельная Node contract проверка — 2/2.
