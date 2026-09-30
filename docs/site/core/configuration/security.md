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
Неверная цепочка, issuer, подпись, срок, номер или отозванный serial закрывает
новое peer connection. Обновление CRL закрывает существующие каналы и требует
нового handshake; insecure downgrade запрещён. Peer transport contract — только в
[pluginprotocol](https://github.com/Liapoldus/pluginprotocol).

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
