# Core CLI

CLI управляет только Core и дополняет Management API. Он запускает Core,
bootstrap-ит первый platform-admin ключ и выполняет SQLite backup/restore;
в v1 не устанавливает, не запускает и не останавливает plugin processes или
containers.

CLI не предоставляет Caddy-specific commands. Caddy settings передаются через
generic plugin configuration API; site releases принадлежат Server plugin Admin
Surface.

- [`serve`](serve) — запуск Core и восстановление active state.
- `database backup <path>` — онлайн-снимок SQLite через `VACUUM INTO`; путь
  назначения должен быть новым. Backup получает права только владельца и
  проходит schema-version, integrity и foreign-key проверки до публикации.
- `database restore <path>` — проверяет backup и атомарно заменяет Core SQLite.
  Core должен быть остановлен; активный `serve` удерживает эксклюзивную блокировку
  state-файла, поэтому restore завершится конфликтом, не меняя БД.
- [`versions`](versions) — поколения plugin-конфигурации и plugin-owned site releases.
- [Management API](../api/) — конфигурация, operations, access и audit.
