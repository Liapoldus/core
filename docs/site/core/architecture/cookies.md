# Cookie boundary — v2

Эта страница описывает отложенную возможность v2. Cookie allow-list API,
хранение cookie policy в Core и распространение её через plugin generations не
входят в Core v1 и отсутствуют в v1 Management API.

В v1 plugin, которому принадлежат cookie semantics, задаёт обычные и `HttpOnly`
cookies через собственный типизированный response contract. Server plugin
проверяет response целиком до отправки HTTP headers или WebSocket upgrade и
сохраняет атрибуты `Set-Cookie`; `pluginprotocol` остаётся транспортом
непрозрачных plugin-defined payloads и не определяет HTTP cookies.

Для v2 требуется отдельно спроектировать per-instance/capability allow-list,
версионирование и атомарную активацию policy. До принятия и реализации такого
контракта не добавлять `/api/plugins/{id}/cookie-policies/*`, SQLite policy
tables или тесты, объявляющие функцию частью v1.

`HttpOnly` запрещает JavaScript страницы читать cookie, но не мешает браузеру
посылать её в последующих подходящих запросах. Владельцем значения и lifecycle
cookie остаётся plugin; посредник не должен раскрывать cookie values в logs,
traces, audit, diagnostics или errors.
