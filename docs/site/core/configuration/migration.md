# Миграция legacy-конфигурации

Эта процедура относится к pre-v2 legacy YAML и не является частью Core runtime
CLI. В Core v2 migration orchestration принадлежит standalone `liapoldus` CLI;
Core предоставляет только versioned migration implementation/endpoint. CLI не
открывает SQLite напрямую.

Production Core получает путь к SQLite через `CORE_SQLITE_PATH`. Первичные
настройки передаются через `CORE_INIT_*` только при первом старте Core;
последующие
изменения выполняются через versioned Settings API и сохраняются в SQLite.
Runtime не читает конфигурационные файлы и не ищет их по каталогам.

До завершения v2 cutover старый YAML принимается временным offline utility:

```sh
core-migrate --input /path/to/core.yaml --dry-run
core-migrate --input /path/to/core.yaml --apply
```

`--dry-run` валидирует YAML, преобразование в typed Core settings и печатает
план, не открывая SQLite на запись. `--apply` берёт exclusive SQLite lock,
создаёт и проверяет backup до schema migration, затем одной транзакцией
инициализирует первую Core settings revision. При неуспешном применении старая
SQLite восстанавливается из backup автоматически; backup сохраняется и при
успехе, и при rollback. Уже инициализированная settings store повторно не
импортируется. Ссылки на secret-файлы переносятся как абсолютные пути;
содержимое файлов не читается.

YAML `plugins` больше не является источником runtime membership: dry-run и
результат импорта перечисляют игнорируемые static instance IDs. После перехода
plugin replicas должны заново пройти authenticated registration. Существующие
plugin settings, точные bytes generations, audit rows и прочие SQLite-данные
не декодируются и не переписываются. Импортируемая Core revision сначала
остаётся pending; следующий старт Core валидирует и применяет её обычным
startup path. В v2 этот workflow вызывается через `liapoldus core migrate` и
target adapter; отдельный `cmd/core-migrate` удаляется из Core после появления
эквивалентного cross-repository contract test.
