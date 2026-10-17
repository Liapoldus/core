# OpenAPI Management API

Машиночитаемый Core contract находится в
[public/spec/management.openapi.yaml](/spec/management.openapi.yaml).
Core endpoints управляют service desired state, settings apply, зарегистрированной
replica membership, Admin Surface, operations, access и audit.
Service-to-service interaction и cookie policies не входят в Core Management API.
API не предоставляет installation или
process/container lifecycle operations для сервисов.

Traffic settings сохраняются как raw service-owned JSON document; Server service
получает точную generation через REST `Reload` и самостоятельный config pull.
Site releases и сертификаты управляются объявленной service Admin Surface.
Caddy Admin API не проксируется Management API.

Versioned config delivery contract: [Config bundles](config-bundles).

API требует TLS и Bearer authorization; web Controller дополнительно
подключается по mTLS. Public error semantics находятся в
[errors.json](/spec/errors.json).

Runtime metadata endpoints являются Core-owned Management API resources. CLI
передаёт `artifactId`, version/digest, instance/replica IDs, control/peer
endpoints и runtime status через registration. Core сам выполняет SDK REST
calls к live service; прямой CLI→service REST path отсутствует.
