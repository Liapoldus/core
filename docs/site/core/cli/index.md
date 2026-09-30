# Core CLI

CLI управляет только Core и дополняет Management API. Он проверяет
status/operations и bootstrap доступ; в v1 не устанавливает, не запускает и не
останавливает plugin processes или containers.

CLI не предоставляет Caddy-specific commands. Caddy settings передаются через
generic plugin configuration API; site releases принадлежат Server plugin Admin
Surface.

- [`serve`](serve) — запуск Core и восстановление active state.
- [`versions`](versions) — поколения plugin-конфигурации и plugin-owned site releases.
- [Management API](../api/) — конфигурация, operations, access и audit.
