# Bootstrap и запуск

При запуске Core получает из environment только абсолютный путь к SQLite:

```sh
export CORE_SQLITE_PATH=/var/lib/liapoldus/core.sqlite
liapoldus core start --target local
```

Standalone `liapoldus core start` передаёт Core `CORE_SQLITE_PATH` и secure
bootstrap references. Core однократно создаёт SQLite settings revision до
открытия API; Core binary не принимает subcommands и не содержит migration,
backup или database commands. Секреты передаются CLI как ссылки на
файлы/монтирования, а не как значения.

Повторный bootstrap для существующей базы завершается конфликтом. Для
изменения настроек после инициализации используется versioned Settings API;
Core хранит desired и effective revisions отдельно. Изменения, которым нужен
restart, остаются pending до явного перезапуска оператором.

Plugin instances входят в membership после аутентифицированной регистрации и
активной lease. Static endpoint registry и файловые override не читаются.
См. [Settings API](../api/config), [миграцию](migration) и
[модель регистрации replicas](../architecture/plugin-deployment).
