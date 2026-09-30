# Развёртывание Core v1

Core v1 состоит из трёх вручную запускаемых сервисов — Core, Server plugin и
forms-db plugin — и двух общих библиотек: Plugin SDK и `pluginprotocol`.
Docker/Compose, Swarm, Kubernetes и автоматический local-process lifecycle
отложены до v2. Нормативная модель — [запуск плагинов](../architecture/plugin-deployment).

## Установка и запуск

Оператор устанавливает Core и каждый plugin binary отдельно. Порядок запуска:

1. Настроить и запустить Core с его SQLite и Management API.
2. Установить/настроить persistent directory и вручную запустить Server plugin.
3. Вручную запустить forms-db plugin.
4. Зарегистрировать fixed endpoints и ожидаемые mTLS identities плагинов через
   Management API, загрузить их конфигурации и дождаться readiness/ACK.

Core не устанавливает, не запускает, не останавливает, не перезапускает,
масштабирует и не удаляет plugin processes или containers. Автозапуск и
перезапуск после сбоя настраиваются оператором средствами ОС. После ручного
рестарта plugin Core заново проверяет identity, Manifest, health и generation;
пользовательские вызовы и оборванные streams не воспроизводятся.

## Persistent state и резервное копирование

Core и каждый plugin имеют отдельные persistent directories. Core backup
включает SQLite, конфигурационные `active`/`previous`, audit и durable
operations. Оператор отдельно резервирует Caddy ACME/site storage и forms-db
данные согласованно с их plugin runbooks. Core не хранит plugin release
packages и не принимает provider credentials.

Restore выполняется оператором: остановить сервисы вручную, восстановить Core
database и соответствующие plugin-owned data, затем запустить Core и плагины.
Core проверяет exact config generations и повторно инициирует `Reload`; он не
выполняет process orchestration. Подробная процедура находится в
[backup/restore](backup-restore).

## Сеть и безопасность

Management API отделён от public listeners Caddy. Core↔plugin REST использует
per-replica mTLS; trust roots и credentials плагинов выдаются оператором и
хранятся отдельно от Core service keys. Публичные HTTP/TLS ports открывает
Server plugin. Peer network доступна только согласно explicit deny-by-default
policy через `pluginprotocol`.

Детали deployment modes, container providers и автоматического управления
plugin processes относятся к v2 и не являются частью v1 acceptance.
