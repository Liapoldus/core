# Bootstrap и запуск

При запуске Core получает из environment только абсолютный путь к SQLite:

```sh
export CORE_SQLITE_PATH=/var/lib/liapoldus/core.sqlite
liapoldus core start --target local
```

`liapoldus core start` запускает Core как Docker container и передаёт ему
`CORE_SQLITE_PATH` и secure bootstrap references. Core однократно создаёт SQLite
settings revision до открытия API; Core binary не принимает subcommands и не
содержит migration, backup или database commands. TLS material передаётся как
file mounts, а не как значения. Management listener доступен CLI через
loopback-only published port; plugin-control listener доступен только внутри
target Docker network.

Для первичного local bootstrap CLI генерирует management token и сохраняет его
в системном credential vault. Token передаётся в Core только через одноразовый
файл с правами `0600` в приватном Docker bind mount. Он никогда не задаётся
значением Docker environment или аргументом процесса. Core проверяет regular
file и permissions, читает и удаляет его до открытия Management API; в SQLite
остаётся только verifier. Docker restart после bootstrap не требует повторного
файла и не ротирует credential.

Повторный bootstrap для существующей базы завершается конфликтом. Для
изменения настроек после инициализации используется versioned Settings API;
Core хранит desired и effective revisions отдельно. Изменения, которым нужен
restart, остаются pending до явного перезапуска оператором.

Plugin instances входят в membership после аутентифицированной регистрации и
активной lease. Static endpoint registry и файловые override не читаются.
См. [Settings API](../api/config) и
[модель регистрации replicas](../architecture/plugin-deployment).
