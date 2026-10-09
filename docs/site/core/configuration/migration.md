# Миграция конфигурации

Production Core получает путь к SQLite через `CORE_SQLITE_PATH`. Первичные
настройки передаются через `CORE_INIT_*` только при `core init`; последующие
изменения выполняются через versioned Settings API и сохраняются в SQLite.
Runtime не читает конфигурационные файлы и не ищет их по каталогам.

Старый YAML принимается только офлайн-командой:

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
остаётся pending; `core serve` валидирует и применяет её обычным startup path.
