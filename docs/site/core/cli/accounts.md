# Service keys

В Core v1 управление доступом через CLI ограничено локальной bootstrap-командой
`core access bootstrap`. Management API позволяет выпускать service key и
получать только его metadata; raw token показывается один раз при выпуске и не
возвращается при чтении. Срок действия проверяется при каждом запросе.

Отдельные операции rotation/revocation service key и команды для них не входят
в v1. Не описывать их как реализованные и не добавлять до открытия соответствующей
задачи следующей версии. Точная текущая API-поверхность приведена в
[OpenAPI](../api/openapi).

Constructor хранит credentials только в server-side secret storage или в OS
credential store; browser не получает Core service key. Credentials не входят
в plugin settings и никогда не выводятся в logs, audit или errors.
