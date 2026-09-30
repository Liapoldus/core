# Применение TLS и traffic settings

Server plugin получает желаемую traffic/TLS configuration как versioned JSON
из Core SQLite через REST config pull после `Reload`. Плагин проверяет candidate, строит private
Caddy runtime config и применяет его без изменения Core active generation до
успеха. Caddy Admin API не является пользовательским endpoint и не имеет
Management pass-through.

ACME issuance/renewal и readiness сертификата принадлежат Server plugin и
CertMagic. В v1 у него одна active replica и persistent filesystem для
сертификатов; Core не хранит Caddy certificate state и не предоставляет
`/api/tls` endpoints. Site artifacts и `current/previous` также принадлежат
plugin storage.

Ошибка plugin apply сохраняет прежний Core config generation и переводит
operation в failure/degraded status. См.
[REST Reload](../architecture/control-plane),
[Caddy ownership](../architecture/target) и
[Management operations](../api/operations).
