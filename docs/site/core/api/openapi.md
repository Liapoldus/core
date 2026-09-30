# OpenAPI Management API

Машиночитаемый Core contract находится в
[public/spec/management.openapi.yaml](/spec/management.openapi.yaml).
Core endpoints управляют plugin desired state, settings apply, заранее
зарегистрированными fixed endpoints, Admin Surface, operations, access и audit.
Plugin-to-plugin interaction и cookie policies не входят в Core Management API
v1. В v1 API не предоставляет installation или
process/container lifecycle operations для плагинов.

Traffic settings сохраняются как raw plugin-owned JSON document; Server plugin
получает точную generation через REST `Reload` и самостоятельный config pull.
Site releases и сертификаты управляются объявленной plugin Admin Surface.
Caddy Admin API не проксируется Management API.

API требует TLS и Bearer authorization; web Controller дополнительно
подключается по mTLS. Public error semantics находятся в
[errors.json](/spec/errors.json).
