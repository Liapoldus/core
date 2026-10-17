# Резервное копирование и восстановление Core

Core не владеет процессами service workloads и не содержит backup/restore CLI.
Оператор управляет этими операциями через универсальный `liapoldus` CLI,
который использует target adapter. Core предоставляет только SQLite schema,
integrity gates и state lock. См. [границы ответственности](../architecture/target).

## Что резервировать

| Данные | Владелец | Правило |
| --- | --- | --- |
| SQLite Core | Core; backup/restore — CLI | Включает service instances, точные JSON bytes поколений `active`/`previous`/`staging`, operations и audit. `staging` нужен для восстановления незавершённой операции и не выдаётся сервису. Файловый snapshot создаёт универсальный CLI через target adapter. |
| Core bootstrap и binary | Оператор | Сохраните версию Core и references на внешние secret/TLS mounts. Не помещайте secret bytes в обычный архив. |
| Server service state | Server service | Отдельно резервируйте persistent data: ACME/certificate state, site releases и `current`/`previous`. |
| forms-db data | forms-db service | Используйте процедуру резервирования и восстановления, определённую владельцем service. |
| TLS identities и secret sources | Оператор/внешний CA | Защищённое хранилище отдельно от SQLite backup; проверьте доступность ссылок и возможность перевыпуска сертификатов. |

Core backup не содержит service-owned базы, site artifacts, сертификаты или
private keys. Service data без соответствующего Core backup может не совпасть с
желаемой конфигурацией. Зафиксируйте для каждого набора timestamp, версии
сервисов, SQLite schema version и digest Core `active` generation; секреты и
сырые service payloads в backup manifest не включайте.

## Breaking-переход к Services: SQLite v15

Перед обновлением сохраните согласованный backup. При открытии старой базы Core
сначала выполняет прежние upgrades, затем применяет отдельную транзакционную
миграцию v15. Она переименовывает runtime-таблицы и индексы в `service_*`,
сохраняя instance/replica/incarnation IDs, replicas, leases, rollout targets и
peer-link policy. Исходные migrations не переписываются.

JSON bytes и SHA-256 поколений `active`/`previous`/`staging`, manifest bytes,
завершённые operation reports и audit history остаются неизменными. Для Core
operational settings создаются новые immutable revisions с `serviceControl`;
прежние revisions сохраняются. Desired/effective pointers остаются раздельными.
Известные kinds незавершённых операций переводятся в Service-имена для recovery.
Ошибка миграции v15 откатывает всю её транзакцию; повторное открытие уже
обновлённой базы не создаёт дополнительные revisions.

Новые Management routes — `/api/services` и `/api/service-links`. Старые HTTP
paths и прежние имена runtime-сервисов не поддерживаются. Старый Core binary не принимает
schema v15; возврат к нему требует восстановления предварительного backup по
процедуре ниже, а не изменения migration history вручную.

## Создание backup

1. Убедитесь, что Core доступен, текущая `active` generation подтверждена
   подключёнными replicas, а операции не находятся в неизвестном состоянии.
2. На время согласованного backup приостановите административные изменения.
3. Выполните `liapoldus core database backup --target <target> --output <destination>` в CLI-репозитории.
   Команда создаёт согласованный online backup через SQLite `VACUUM INTO`,
   проверяет текущую schema version, `quick_check(1)` и `foreign_key_check`,
   выставляет права `0600` и публикует файл только если destination ещё не
   существует. Не копируйте `.db` вручную вместе или без WAL-файлов.
4. Отдельно сохраните Caddy и forms-db data их штатными средствами. Не считайте
   копию активного service store согласованной без его backup procedure.
5. Зафиксируйте версии, digests и состав файлов. Проверьте архив и периодически
   выполняйте пробное восстановление в изолированной среде.

## Восстановление

1. Восстанавливайте сначала в изолированное окружение. Проверьте целостность
   SQLite, совместимость Core binary и наличие внешних secret/TLS references.
   При старте Core выполняет SQLite `quick_check(1)` и `foreign_key_check` до и
   после применения схемы; любой отказ не допускает открытия listener-ов. Эти
   проверки не заменяют пробное восстановление полной резервной копии.
2. Остановите Core через target adapter и выполните
   `liapoldus core database restore --target <target> --input <backup.sqlite>` в CLI-репозитории. Команда
   повторно проверит schema version, integrity и foreign keys, скопирует файл во
   временный файл рядом с target и атомарно заменит SQLite. Если Core работает,
   exclusive state lock отклонит restore, не меняя текущую базу.
3. Восстановите SQLite. Core должен загрузить `active` и
   `previous` из долговременного хранилища и собрать runtime snapshot в памяти.
4. Вручную запустите Caddy и forms-db из совместимых operator-managed binary
   releases, используя их восстановленные persistent data.
5. Дождитесь mTLS handshake, health и ACK `active` generation от каждой
   обязательной replica. При запуске Core выполняет startup reconciliation и
   может повторно вызвать REST `Reload` для отстающей replica; service сам
   запрашивает точное поколение. В работающем Core фонового повторного Reload
   нет. Не воспроизводите service-to-service calls с неизвестным результатом.
6. Проверьте audit, config digests, Caddy listeners/TLS/site state и forms-db
   data. Только после успешного smoke test открывайте административный доступ и
   публичный traffic.

Не редактируйте SQLite вручную и не подменяйте `active`/`previous` каталогами.
При несовпадении generation оставьте затронутую replica fenced и используйте
поддерживаемые Core rollback/recovery операции.

## Обновление и аварийное восстановление

Обновляйте Core, Service SDK, `wool`, Caddy и forms-db последовательно,
проверяя объявленную совместимость до следующего компонента. Все сервисы
запускаются оператором; Core не устанавливает, запускает, останавливает,
перезапускает или масштабирует service workload.

Откат конфигурации через Core перемещает сохранённые `active` и `previous`
поколения и инициирует reload. Это не откатывает бинарники и service-owned data.
Откат Caddy/forms-db binary выполняет оператор по их процедурам, сохраняя
совместимость с текущей конфигурацией.

| Событие | Действие |
| --- | --- |
| Service недоступен | Проверьте endpoint, сертификат и health; вручную восстановите только этот сервис. Затронутые bindings остаются unavailable. |
| Core перезапущен | Core восстанавливает SQLite snapshot и повторяет уведомление о `active`; не открывайте зависимые функции до ACK. |
| Потеряно Caddy storage | Восстановите service-owned ACME/site data и проверьте listeners до открытия traffic. |
| SQLite повреждена или миграция не прошла | Остановите Core, сохраните исходную копию и восстановите проверенный backup; не создавайте пустую БД поверх неё. |
| Secret reference недоступен | Исправьте внешний secret source или перевыпустите credential. Не помещайте secret bytes в config, CLI arguments, logs или audit. |

Проверки готовности и operations см. в [наблюдаемости](observability), а
обязательные release gates — в [матрице приёмки](../configuration/acceptance).
