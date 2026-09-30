# Каталог ошибок

Публичные error codes и `application/problem+json` shape заданы в
[errors.json](/spec/errors.json) и
[Management OpenAPI](/spec/management.openapi.yaml). Внутренние Go errors не
являются публичным contract и не должны раскрывать implementation details.

Каждый Management API Problem содержит `type`, локализованные `title`, HTTP
`status`, стабильный `code`, безопасный `detail`, `instance` и `requestId`;
необязательный `path` не включает query или credentials. Пара `status/code` и
публичные тексты берутся только из versioned error catalog. Ошибка workload
TLS возникает до HTTP и не имеет Problem response. Durable operation сохраняет
только безопасный `errorCode`; внутренний cause, сырые plugin diagnostics и
payload не возвращаются через polling.

Config apply, plugin install и generation activation при отказе сохраняют
предыдущую active revision. Безопасная диагностика не содержит секретных
значений, plugin payload, private key, raw endpoint credential или локальных
secret paths. Plugin Admin failures преобразуются в стабильные публичные
ошибки, а successful response проходит проверку объявленного action schema;
неизвестный внутренний код нормализуется к безопасной общей ошибке. Caddy-specific
Admin/checkpoint/drift errors удалены вместе со старой Core API поверхностью.

Текущие расхождения runtime с нормативным mapping и непокрытые operations
отслеживаются в [acceptance matrix](../configuration/acceptance); описанный
здесь контракт не означает, что эти gates уже реализованы.
