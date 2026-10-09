# Bootstrap и запуск

При запуске Core получает из environment только абсолютный путь к SQLite:

```sh
export CORE_SQLITE_PATH=/var/lib/liapoldus/core.sqlite
core init
core serve
```

`core init` однократно создаёт SQLite и начальную revision собственных настроек.
Первоначальные значения можно передать через `CORE_INIT_MANAGEMENT_LISTEN`,
`CORE_INIT_MANAGEMENT_CERTIFICATE`, `CORE_INIT_MANAGEMENT_KEY`,
`CORE_INIT_MANAGEMENT_CLIENT_CA`, `CORE_INIT_CONTROL_LISTEN`,
`CORE_INIT_CONTROL_PUBLIC_URL`, `CORE_INIT_CONTROL_CERTIFICATE`,
`CORE_INIT_CONTROL_KEY`, `CORE_INIT_REPLICA_CLIENT_CA`,
`CORE_INIT_REPLICA_SERVER_CA` и `CORE_INIT_SECRET_ROOT`. Параметры `CORE_INIT_*`
читаются только при первичной инициализации. Секреты передаются как ссылки на
файлы/монтирования, а не как значения.

Повторный `core init` для существующей базы завершается конфликтом. Для
изменения настроек после инициализации используется versioned Settings API;
Core хранит desired и effective revisions отдельно. Изменения, которым нужен
restart, остаются pending до явного перезапуска оператором.

Plugin instances входят в membership после аутентифицированной регистрации и
активной lease. Static endpoint registry и файловые override не читаются.
См. [Settings API](../api/config), [миграцию](migration) и
[модель регистрации replicas](../architecture/plugin-deployment).
