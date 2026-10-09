# Развёртывание Core v1

Core v1 состоит из трёх вручную запускаемых сервисов — Core, Server plugin и
forms-db plugin — и двух общих библиотек: Plugin SDK и `pluginprotocol`.
Docker/Compose, Swarm, Kubernetes и автоматический local-process lifecycle
отложены до v2. Нормативная модель — [запуск плагинов](../architecture/plugin-deployment).

## Установка и запуск

Оператор устанавливает Core и каждый plugin binary отдельно. Порядок запуска:

1. Подготовить `core.yaml`: в нём оператор заранее объявляет каждый plugin
   instance, его фиксированные replica endpoints и ожидаемые mTLS identities;
   настроить отдельные trust roots и сертификаты Core и плагинов.
2. Настроить persistent directories и вручную запустить Server и forms-db
   plugin binaries. Каждый plugin сам поднимает свой REST endpoint; Core не
   устанавливает и не запускает эти процессы. До продолжения проверить их
   control-plane health и mTLS identity.
3. Запустить Core с `core.yaml`, SQLite и Management API. При старте Core
   регистрирует объявленную топологию в SQLite и однократно сверяет активные
   поколения с уже запущенными replicas. Недоступная replica видна как
   degraded и не блокирует запуск Core.
4. Через Management API создать/обновить plugin settings и дождаться
   завершения operation и readiness/ACK нужных replicas.

Core не устанавливает, не запускает, не останавливает, не перезапускает,
масштабирует и не удаляет plugin processes или containers. Автозапуск и
перезапуск после сбоя настраиваются оператором средствами ОС. После ручного
рестарта plugin Core заново проверяет identity, Manifest, health и generation.
Если Core оставался запущен, его read-only readiness monitor отмечает replica
как degraded, но не повторяет `Reload`; после проверки health оператор вручную
перезапускает Core для startup reconciliation. Пользовательские вызовы и
оборванные streams не воспроизводятся.

## Persistent state и резервное копирование

Core и каждый plugin имеют отдельные persistent directories. Core backup
включает SQLite, конфигурационные `active`/`previous`, audit и durable
operations. Оператор отдельно резервирует Caddy ACME/site storage и forms-db
данные согласованно с их plugin runbooks. Core не хранит plugin release
packages и не принимает provider credentials.

Restore выполняется оператором: остановить сервисы вручную, восстановить Core
database и соответствующие plugin-owned data, затем запустить плагины и Core.
Core проверяет exact config generations и повторно инициирует `Reload`; он не
выполняет process orchestration. Подробная процедура находится в
[backup/restore](backup-restore).

## Сеть и безопасность

Management API отделён от public listeners Caddy. Core↔plugin REST использует
per-replica mTLS; trust roots и credentials плагинов выдаются оператором и
хранятся отдельно от Core service keys. Публичные HTTP/TLS ports открывает
Server plugin. Peer network доступна только согласно explicit deny-by-default
policy через `pluginprotocol`.

Внешнее размещение Docker/Swarm/Kubernetes, self-registration и rollout — v2;
установка и плановые обновления принадлежат оператору. Core не выполняет process
supervision и не масштабирует по нагрузке. Эти функции не входят в v1 acceptance.

## Команды оператора

Ниже — форма команд, а не готовые production credentials. До запуска оператор
создаёт `core.yaml`, выдаёт разные сертификаты для Core control listener,
каждой plugin REST replica и plugin↔plugin peers, настраивает соответствующие
trust roots/CRL и сохраняет абсолютные пути в командах плагинов. DNS SAN Core
сертификата должен соответствовать `--core-server-name`; plugin REST certificate
CN/URI должны совпадать с `expectedPeerIdentity`, а ожидаемые Core CN/URI — с
SDK-флагами и SAN Core-сертификатов. Подробные обязательные флаги принадлежат
руководствам [Server](/plugins/server) и
[forms-db](/plugins/forms-db).

Создать первый Management service key следует до постоянного запуска Core;
токен показывается один раз и передаётся в защищённое хранилище оператора:

```bash
./core --config /absolute/path/to/core.yaml access bootstrap
```

Затем вручную запустить каждый plugin binary командами из его руководства и
запустить Core:

```bash
./core --config /absolute/path/to/core.yaml serve
```

Проверить Management readiness, plugin inventory и подтверждённое активное
поколение. Вызовы между Server и forms-db используют отдельный peer listener и
отдельную mTLS trust chain; успешный Core control handshake сам по себе не
проверяет plugin↔plugin доступность. Для остановки оператор штатно посылает
`SIGTERM`: сначала остановить Core, затем плагины; при восстановлении запускать
плагины и Core в порядке, описанном выше. Core не хранит PID и не повторно
запускает процессы.
