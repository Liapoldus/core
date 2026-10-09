# Развёртывание Core

Core запускается как отдельный процесс и хранит собственные настройки и
состояние в SQLite. На старте environment задаёт только путь к базе; параметры
работы Core после инициализации берутся из SQLite. Runtime configuration files
не поддерживаются.

## Первичная инициализация

```sh
export CORE_SQLITE_PATH=/var/lib/liapoldus/core.sqlite
liapoldus core start --target local
```

Core однократно создаёт SQLite settings state из bootstrap references. Core не
имеет собственного CLI; `liapoldus` управляет процессом через target adapter.
Bootstrap полный список переменных и поведение revision описывает в
[руководстве bootstrap](../configuration/bootstrap).

## Plugins и rollout

Core API управляет plugin settings и rollout. Standalone CLI собирает bundle из
Git commit и отправляет его через API. Membership plugin replicas
определяется аутентифицированной регистрацией и активными leases; статический
список endpoints в конфигурационном файле отсутствует. Plugin процессы
разворачивает и перезапускает оператор или отдельная deployment-система.
Транспорт и контракт rollout принадлежат [Plugin SDK](/plugin-sdk/).

## SQLite и восстановление

SQLite содержит собственные настройки Core, plugin settings, generations,
audit и durable operations. Резервную копию создавайте штатной командой при
остановленном Core либо через проверенный SQLite backup API. Перед восстановлением
остановите Core и плагины, восстановите согласованные Core/plugin данные, затем
запустите plugin replicas и Core. При старте Core валидирует effective settings;
невалидная desired revision остаётся pending.

Старые YAML-файлы не входят в runtime backup requirement. Их можно передать
только offline migration tool; см. [миграцию конфигурации](../configuration/migration).
