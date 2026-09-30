# Проверка запроса через plugin capability

Core не содержит специального CAPTCHA action, provider registry или
verification endpoint. Это пример границы архитектуры, а не встроенная функция
Core: WAF может вызвать произвольную capability подключённого plugin, а
сам plugin владеет проверкой, callback-маршрутом и token/cookie lifecycle.

Общее поведение WAF capability и редактирование чувствительных данных описаны
в [разделе безопасности](/core/configuration/security). Продуктовая
конфигурация CAPTCHA, если она реализована в отдельном plugin, описывается
только в [документации этого plugin](/plugins/captcha).

Пример описывает целевую plugin-owned границу, а не доступную сейчас WAF
функцию: WAF runtime не имеет production consumer-а, а `serve` пока не связывает
plugin instances с Caddy dispatch. Статус реализации приведён в
[матрице core](/core/architecture/implementation#текущее-состояние-core).
