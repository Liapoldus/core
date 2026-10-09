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

## Состояние реализации

**Core v1 пока не production-ready.** Состояние ниже сверено 2026-10-02;
детальные владельческие backlogs и команды находятся в `TODO.md` каждого
репозитория. Проверки доказывают сквозной Core→SDK→Server→forms-db путь на
macOS и в Linux/arm64 container, но не закрывают полный release gate.

| Компонент | Текущее подтверждение | Осталось для v1 |
| --- | --- | --- |
| Core | На текущем дереве прошли `make check` (69 файлов / 127 tests), `go vet ./...`, `make staticcheck-u1000`; integration покрывает raw config generations, REST Reload/pull/ACK, rollback, recovery и forwarding. | Полный операторский security/recovery walkthrough, Linux host matrix, hosted CI и согласованный release metadata. |
| Plugin SDK | На текущем дереве прошли `make check`, `go build ./...`, `go vet ./...`; REST lifecycle, mTLS и bounded artifact stream покрыты integration tests. | Финальный release gate consumers, platform evidence и module/version pinning. |
| `pluginprotocol` | На текущем дереве прошли `make check` и `make check-race` (136 tests); surface ограничена generic plugin↔plugin communication. | Платформенная/release conformance и coordinated version pinning; отдельный multi-language implementation относится к v2. |
| Server и forms-db | Их отдельные suites зелёные. Настоящие Core, Server и forms-db binaries прошли сквозной mTLS lifecycle; Server обслужил изменение traffic, опубликовал artifact, а HTTP submit прошёл напрямую в forms-db по peer mTLS. Production Core E2E для PostgreSQL, MySQL и MariaDB также проверяет 24 параллельные записи и cursor pagination. Read-only certificate list/get входит в v1; ACME renew автоматический, ручные renew/revoke actions не входят. | Остаются native Linux и hosted CI checks. |
| Документация | Локальный `docs:sync` и VitePress build прошли на текущих owner checkouts. | Обновить закреплённые remote revisions и проверить удалённую сборку/опубликованные маршруты; до этих действий не считать docs release опубликованным. |

Следующий критический путь — полный operator walkthrough и release/platform gates из
[матрицы приёмки](../configuration/acceptance).
CAPTCHA и Identity остаются вне v1.
