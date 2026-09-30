# Размещение plugins

## Core v1: ручное размещение

Оператор устанавливает и запускает Core и каждый plugin самостоятельно.
Core подключается к зарегистрированным REST endpoints по mTLS и управляет
только generic состоянием: settings generations, Reload, health, grants,
policy и audit. Core не устанавливает, не запускает, не останавливает, не
перезапускает, не масштабирует и не удаляет plugin workloads.

Локальный процесс, контейнер Docker, Swarm service или Kubernetes Pod могут
использоваться оператором как способ ручного размещения, но v1 Core не имеет
provider API и не принимает ответственность за workload. Operator задаёт
фиксированный endpoint и expected replica identity. После restart Core
повторяет handshake и сверяет health/config generation; он не запускает
бинарник и не повторяет неизвестный plugin call.

## Отложено до v2

В v2 можно отдельно спроектировать и реализовать следующие managed deployment
варианты. Ни один из них не является режимом Core v1 и не должен попадать в его
API, SQLite schema, permissions или acceptance:

| Вариант | Будущая ответственность Core | Обязательная граница владения |
| --- | --- | --- |
| Local supervised process | Возможная установка/запуск/restart по согласованной release policy | Только процессы и файлы, явно созданные Core; без управления произвольными host workloads |
| Managed Docker/Compose/Swarm/Kubernetes | Возможное создание/обновление workload через provider adapter | Изменять/удалять можно только Core-owned resources; внешние workloads остаются неизменными |
| External workload | Только регистрация endpoint, mTLS identity, health и конфигурация | Core никогда не устанавливает, запускает, перезапускает, масштабирует или удаляет external workload |

До начала v2 эти варианты не требуют provider credentials, Docker socket,
Kubernetes client или TUF metadata в Core.

## Одинаковый lifecycle во всех будущих вариантах

Способ размещения не меняет plugin contract. Core хранит точные JSON bytes и
поколения `active`/`previous`; после проверки candidate он атомарно обновляет
SQLite и вызывает REST `Reload(generation)`. Plugin сам pull-ит конкретное
поколение через Plugin SDK и подтверждает digest. Частичная доставка идёт
roll-forward; неподтвердившая replica degraded/fenced. Эти гарантии v1 уже
задаются без Core-managed process control.

Полная целевая архитектура и текущие gates описаны в
[архитектуре Core](target) и [acceptance matrix](../configuration/acceptance).
