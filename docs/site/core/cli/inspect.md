# CLI диагностика

CLI отображает здоровье SQLite, registered endpoints и replica health,
состояние конфигурационных поколений и durable operations без секретов или raw
plugin payloads. В v1 Core не
предоставляет `group`, `caddy`, `drift` или `checkpoint` commands; Caddy-specific
traffic/site operations принадлежат Server plugin Admin Surface.

CLI не имеет plugin process/container lifecycle команд: запуск и перезапуск
плагинов выполняет оператор. Точный CLI contract будет
синхронизирован с code-owned flags/defaults в ходе implementation; текущий
roadmap — [здесь](../architecture/v1-migration-roadmap).
