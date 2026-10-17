# Наблюдаемость Core и services

Core предоставляет ограниченную наблюдаемость control plane:

- `GET /healthz` сообщает, что процесс Core отвечает.
- `GET /api/status` возвращает readiness и drift по подключённым service
  replicas.
- Standalone `liapoldus status` и `liapoldus operation watch` получают через
  Management API состояние SQLite-derived generations, endpoints и durable
  operations в пределах безопасной redacted-модели.
- Management audit хранится в SQLite и фиксирует actor, действие, ресурс,
  operation ID, digest, timestamp, результат и request ID.

Core не предоставляет Prometheus `/metrics`, экспорт трассировок,
настраиваемые log sinks или централизованный сбор логов services. Не
настраивайте scrape или alert на несуществующем Core metrics endpoint. Для
проверки readiness используйте `/healthz`, `/api/status` и standalone CLI;
доступ к Management API защищён согласно
[модели безопасности](../api/authentication).

Service SDK предоставляет собственный lifecycle metrics endpoint и пишет
структурированные JSON lifecycle logs в stdout/stderr каждого service process.
Общий SDK-контракт описан в [документации Service SDK](/service-sdk/).
Server и forms-db остаются отдельными источниками этих данных: Core не
агрегирует их metrics/logs и не включает product-specific состояния, например
ACME readiness или HTTP traffic, в собственную модель. Инструкции и границы
SDK описаны в его owner-документации; детали продуктовых данных — в
документации соответствующего service.

Audit и диагностика не должны содержать settings plaintext, service payloads,
Authorization, secret values/references, private keys, cookie values или grant
handles. Для восстановления Core и резервирования SQLite используйте
[backup/restore runbook](backup-restore).
