# Миграция legacy-конфигурации

Эта процедура относится к pre-v2 legacy YAML и полностью принадлежит standalone
`liapoldus` CLI. Core runtime не читает YAML и не предоставляет migration
entrypoint. Для local target CLI использует собственный execution adapter, для
remote target — только согласованный Management API.

Production Core получает путь к уже подготовленной SQLite через
`CORE_SQLITE_PATH`. Изменения выполняются через versioned Settings API и
сохраняются в SQLite.
Runtime не читает конфигурационные файлы и не ищет их по каталогам.

CLI принимает старый YAML только как одноразовый вход внешней команды:

```sh
liapoldus core migrate --target local --input /path/to/core.yaml --dry-run
liapoldus core migrate --target local --input /path/to/core.yaml --apply
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
startup path. Команда находится только в репозитории CLI; в Core нет
`cmd/core-migrate` и дублирующей SQLite migration implementation.
