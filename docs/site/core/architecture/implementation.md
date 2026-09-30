# Статус реализации Core v1

Это краткий статус целевой архитектуры, не журнал команд или прошлых запусков.
Предыдущие проверки засчитываются только если доказывают текущее поведение REST
lifecycle и новых config generations. Актуальные критерии и формат evidence описаны в
[матрице acceptance](../configuration/acceptance); по репозиториям — в их
`TODO.md`.

## Зафиксировано

- Core должен быть singleton plugin-agnostic control plane; Caddy data plane
  принадлежит независимому plugin.
- Core↔plugin lifecycle переходит на отдельный REST Plugin SDK; Core не зависит
  от `pluginprotocol`.
- `pluginprotocol` ограничен generic plugin↔plugin transport/communication.
- Plugin settings представлены прямым plugin-owned JSON object, хранятся в
  Core как точный raw JSON BLOB, а не как нормализованная Go-модель.
- На instance используются durable slots `active`, `previous` и внутренний
  `staging`. Candidate сохраняется в `staging` для recovery; plugin может
  pull-ить только `active`/`previous`. Promotion переносит candidate в `active`,
  прежний `active` в `previous`, удаляя старый `previous`. Partial rollout
  выполняется roll-forward с ACK для каждой replica.
- Constructor и `react-lib` заморожены.

## Состояние миграции

**Core v1 не готов: изолированные gates модулей зелёные, consumer integration
ещё красная.** Состояние ниже сверено 2026-09-30; подробные владельческие
backlogs и команды находятся в `TODO.md` каждого репозитория.

| Компонент | Текущее подтверждение | Осталось для v1 |
| --- | --- | --- |
| Core | `make check`, `go vet ./...`, `make staticcheck-u1000` прошли. Добавлены REST composition, declared replica clients и SQLite replica observations. | In-memory snapshot, SQLite integrity/backup/restore, secret grant endpoints, reconnect/failure/recovery и сквозные security tests. |
| Plugin SDK | `make check` прошёл: 176 TypeScript tests, `go build ./...`, `go vet ./...`. | Интеграция с активными consumers; canonical module path/repository и Linux runtime evidence. |
| `pluginprotocol` | `make check` прошёл: 136 TypeScript tests, `go vet ./...`, `go build ./...`; публичная поверхность generic peer-to-peer. | Активным plugins нужно удалить обращения к удалённым lifecycle exports; отдельный multi-language implementation относится к v2. |
| Server и forms-db | Изменения контрактов и SDK adapters существуют в локальных commit-ах. | Текущий общий `go test ./server/... ./forms-db/...` падает на импортированных удалённых protocol packages; Server дополнительно не совпадает с API SDK. Сквозного Core→SDK→plugin smoke нет. |
| Документация | `npm run build` прошёл. | После push новые `/core/` маршруты отвечали `404`, тогда как `/gateway/configuration/` отвечал `200`; Pages deployment требует отдельной проверки. |

Следующий критический путь — согласованно довести обе активные plugin migrations до
сборки, затем проверить реальный Core→SDK REST/mTLS→Reload→exact pull→apply→ACK
на Server и forms-db. После этого закрываются Core recovery/security/platform
gates из [матрицы приёмки](../configuration/acceptance). CAPTCHA, Identity,
Constructor и `react-lib` остаются заморожены и вне v1.
