# Границы безопасности Core v3

> Текущим нормативным источником является [архитектура v3](../architecture/v3).
> Ниже зафиксированы применимые trust boundaries; старые v2 deployment claims
> не являются fallback или поддерживаемым runtime.

## Management API

Management listener не размещается на public Caddy listeners. TLS server identity
обязателен всегда. Web Controller соединяется по private HTTPS с mTLS и отдельным
Bearer service credential на binding; credential остаётся на backend и не
передаётся browser. Для desktop доступа используется ограниченный SSH
port-forward к loopback Management API; SSH policy запрещает shell, SFTP и agent
forwarding, а bearer хранится в OS credential store. Core авторизует каждую
операцию, пишет audit и не раскрывает token повторно.

## Service workload

Service control REST и service-to-service network — разные trust domains.
Service SDK защищает Core↔service REST connection; `wool` владеет
только peer-to-peer credentials/transport. Management trust, service-control
REST trust и peer-network trust раздельны. Оператор получает внешние CA
identities отдельно для каждой replica и регистрирует ожидаемую identity вместе
с endpoint. Core не является CA, не запускает service process и не получает
service private keys.

Реализация TLS/mTLS и поддерживаемых plaintext profiles принадлежит
SDK/protocol libraries, а не продуктовым сервисам. Сервис передаёт bootstrap
credentials и явно выбранный security profile в API библиотеки; его handlers
не создают TLS listeners/clients и не проверяют сертификаты самостоятельно.
Текущий production Core↔service REST contract требует per-replica mTLS. SDK
реализует отдельный versioned loopback-only plaintext development profile:
он выключен по умолчанию и открывает только generic `GET /healthz`
на отдельном listener с literal loopback TCP address. `/ready` остаётся под
mTLS из-за replica identity и generation metadata в ответе; redacted readiness
view требует отдельного versioned contract. Exact-generation config pull и
secret-grant endpoints и clients всегда остаются за mTLS и недоступны через
plaintext. Этот профиль не является production transport и не используется как
secure fallback.
Для service↔service remote и production connections используется mTLS;
`wool` уже допускает явный TCP-loopback plaintext
development profile без encryption или peer identity. QUIC всегда зашифрован и
аутентифицирует обе стороны; Unix socket и Windows named pipe в v3 требуют
mTLS. Все plaintext profiles должны быть явно включены и ограничены своими
contract-ом и carrier-ом; secure failure никогда не запускает plaintext
fallback. Trust roots SDK REST и peer protocol не объединяются.

Remote trust использует externally issued identities и signed CRL bundles.
Для Core↔service REST задаются независимые `replicaClientCA` и
`replicaServerCA`; для каждого trust root можно указать свой список
`replicaClientCRLs` или `replicaServerCRLs`. При настроенном CRL Core проверяет
его подпись, issuer и срок действия и отклоняет отозванный serial при каждом
новом TLS handshake. Ошибка чтения или проверки CRL закрывает соединение.
Core загружает эти файлы при старте; замена сертификатов, CA или CRL требует
согласованного orderly restart Core и затронутых service replicas. Горячая
Ротация trust roots требует планового перерыва и в Core v3 не выполняется горячо.

### Плановая замена CA и workload identities

V1 поддерживает замену CA и leaf-сертификатов только с плановым перерывом:

1. Создайте и проверьте комплект новых credentials для Core и каждой service
   replica. URI/CN identities должны совпадать с identity аутентифицированной
   регистрации, а
   новые trust roots должны быть доступны в нужных Core↔service bundle и отдельно
   в service↔service bundle.
2. Согласуйте резервное копирование Core SQLite и product-owned данных сервисов
   по соответствующим процедурам. Не копируйте private keys в Core backup.
3. Остановите Core и все затрагиваемые сервисы штатными средствами оператора.
   Не меняйте CA/CRL файлы при работающих процессах: они не перечитываются
   автоматически.
4. Атомарно замените сертификаты, private keys, trust-root bundles и подписанные
   CRL bundles. Убедитесь, что каждый CRL выдан соответствующим новым issuer и
   содержит корректные serials.
5. Запустите service binaries вручную и дождитесь их HTTPS REST/peer listeners.
   Затем запустите Core. Core восстановит SQLite snapshot и выполнит обычный
   `Reload`/exact-generation pull/ACK.
6. Проверьте `/api/status`: все объявленные replicas должны быть `ready`,
   `drift` — `false`; проверьте реальный Server HTTP route и разрешённый
   Server→forms-db вызов. Проверьте, что соединение со старой CA identity
   отклоняется.

Если любая проверка не проходит, остановите затронутые процессы и восстановите
предыдущий согласованный комплект credentials и trust bundles; не удаляйте
предыдущие данные или certificates до успешной проверки нового набора.

Для service↔service peer transport применяются отдельные trust roots и CRL
механизмы `wool`; их профиль и правила reconnect описаны только в
[контракте wool](https://github.com/Liapoldus/wool).

### Внешний traffic-controller v2

Внешний traffic-controller подключается к отдельному private mTLS listener Core,
а не к пользовательскому Management API, и не получает `platform-admin` service
key. Его API аутентифицирует отдельную mTLS identity с минимальными правами:
прочитать выданный Core rollout intent и подтвердить фактически применённую
ступень веса. Identity не может редактировать сам rollout, конфигурации сервисов,
peer-link policy, service keys или другие Management resources. Подтверждение
применяется только к ожидаемой revision через CAS и записывается в audit.

Для controller listener задаются собственные bind, server credentials, client
trust roots и allow-list точных certificate identities; они раздельны с
Management API, Service SDK REST и `wool`. Неизвестный, отозванный или
неразрешённый сертификат отклоняется до обработки HTTP-запроса. Bearer token или
plaintext не являются fallback. Listener задаётся в Core Settings API,
сохраняется в SQLite и активируется после безопасного перезапуска Core. GET списка возвращает только
rollout-ы после candidate ACK-барьера, общий ETag и per-rollout revision.
Confirmation использует strong per-rollout `If-Match`, `Idempotency-Key`,
ограниченный JSON body, точный active stage и вес. Повтор дедуплицируется по
identity + key + request digest; receipt и audit сохраняются атомарно. Ошибка не
перемещает stage, а controller не может заменить обязательное ручное одобрение
platform-admin.

## Межсервисные вызовы и secrets

В Core v3 Core хранит generic service-to-service peer-link policies и публикует
caller-scoped peer-directory через Service SDK, но не проксирует межсервисные
вызовы. `wool` предоставляет generic transport; вызывающий service
получает разрешённую запись directory и применяет её к generic authorizer.
Отсутствие разрешения означает deny. Core не проксирует peer payload.

Service REST config pull содержит versioned JSON и opaque secret references, но
не secret bytes. Core выдаёт только ограниченные grants через Service SDK REST;
service держит разрешённое значение только в памяти и очищает его после срока
действия. Raw secrets,
private keys, bearer, cookies, grants, request bodies и приватные filesystem
paths запрещены в логах, errors, traces и audit.

## Process and artifact boundaries

Оператор отвечает за происхождение, проверку и обновление вручную запускаемых
service binaries. Core не принимает binary/OCI packages и не получает provider
credentials. Service process не получает Core SQLite credentials или доступ к
базе. Server service
имеет собственный persistent directory для сертификатов и site releases, но
его settings source of truth остаётся Core SQLite. Docker/Compose, Swarm,
Kubernetes как внешнее размещение и регистрация replicas — v2; Core
process supervision исключён; установку и обновления выполняет оператор.
