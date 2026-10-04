# Границы безопасности Core

## Management API

Management listener не размещается на public Caddy listeners. TLS server identity
обязателен всегда. Web Controller соединяется по private HTTPS с mTLS и отдельным
Bearer service credential на binding; credential остаётся на backend и не
передаётся browser. Для desktop доступа используется ограниченный SSH
port-forward к loopback Management API; SSH policy запрещает shell, SFTP и agent
forwarding, а bearer хранится в OS credential store. Core авторизует каждую
операцию, пишет audit и не раскрывает token повторно.

## Plugin workload

Plugin control REST и plugin-to-plugin network — разные trust domains.
Plugin SDK защищает Core↔plugin REST connection; `pluginprotocol` владеет
только peer-to-peer credentials/transport. Management trust, plugin-control
REST trust и peer-network trust раздельны. В v1 оператор получает внешние CA
identities отдельно для каждой replica и регистрирует ожидаемую identity вместе
с endpoint. Core не является CA, не запускает plugin process и не получает
plugin private keys.

Remote trust использует externally issued identities и signed CRL bundles.
Для Core↔plugin REST задаются независимые `replicaClientCA` и
`replicaServerCA`; для каждого trust root можно указать свой список
`replicaClientCRLs` или `replicaServerCRLs`. При настроенном CRL Core проверяет
его подпись, issuer и срок действия и отклоняет отозванный serial при каждом
новом TLS handshake. Ошибка чтения или проверки CRL закрывает соединение.
Core загружает эти файлы при старте; замена сертификатов, CA или CRL требует
согласованного orderly restart Core и затронутых plugin replicas. Горячая
ротация trust roots в v1 не поддерживается.

### Плановая замена CA и workload identities

V1 поддерживает замену CA и leaf-сертификатов только с плановым перерывом:

1. Создайте и проверьте комплект новых credentials для Core и каждой plugin
   replica. URI/CN identities должны совпадать с объявленными в `core.yaml`, а
   новые trust roots должны быть доступны в нужных Core↔plugin bundle и отдельно
   в plugin↔plugin bundle.
2. Согласуйте резервное копирование Core SQLite и product-owned данных плагинов
   по соответствующим процедурам. Не копируйте private keys в Core backup.
3. Остановите Core и все затрагиваемые плагины штатными средствами оператора.
   Не меняйте CA/CRL файлы при работающих процессах: они не перечитываются
   автоматически.
4. Атомарно замените сертификаты, private keys, trust-root bundles и подписанные
   CRL bundles. Убедитесь, что каждый CRL выдан соответствующим новым issuer и
   содержит корректные serials.
5. Запустите plugin binaries вручную и дождитесь их HTTPS REST/peer listeners.
   Затем запустите Core. Core восстановит SQLite snapshot и выполнит обычный
   `Reload`/exact-generation pull/ACK.
6. Проверьте `/api/status`: все объявленные replicas должны быть `ready`,
   `drift` — `false`; проверьте реальный Server HTTP route и разрешённый
   Server→forms-db вызов. Проверьте, что соединение со старой CA identity
   отклоняется.

Если любая проверка не проходит, остановите затронутые процессы и восстановите
предыдущий согласованный комплект credentials и trust bundles; не удаляйте
предыдущие данные или certificates до успешной проверки нового набора.

Для plugin↔plugin peer transport применяются отдельные trust roots и CRL
механизмы `pluginprotocol`; их профиль и правила reconnect описаны только в
[контракте pluginprotocol](https://github.com/Liapoldus/pluginprotocol).

## Межплагинные вызовы и secrets

В v1 Core не хранит plugin-to-plugin interaction policies и не авторизует
межплагинные вызовы. `pluginprotocol` предоставляет generic transport, а
вызывающий plugin владеет своей policy и передаёт её своему consumer-у;
отсутствие разрешения должно означать deny. Core не проксирует peer payload.
Централизованная policy/interaction API отложена до v2.

Plugin REST config pull содержит versioned JSON и opaque secret references, но
не secret bytes. Core выдаёт только ограниченные grants через Plugin SDK REST;
plugin держит разрешённое значение только в памяти и очищает его после срока
действия. Raw secrets,
private keys, bearer, cookies, grants, request bodies и приватные filesystem
paths запрещены в логах, errors, traces и audit.

## Process and artifact boundaries

Оператор отвечает за происхождение, проверку и обновление вручную запускаемых
plugin binaries. Core не принимает binary/OCI packages и не получает provider
credentials. Plugin process не получает Core SQLite credentials или доступ к
базе. Server plugin
имеет собственный persistent directory для сертификатов и site releases, но
его settings source of truth остаётся Core SQLite. Docker/Compose, Swarm,
Kubernetes и автоматическое управление plugin processes — v2 scope.
