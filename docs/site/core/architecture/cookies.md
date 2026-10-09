# Cookie boundary — v2

Эта страница описывает отложенную возможность после Core v2. Cookie allow-list API,
хранение cookie policy в Core и распространение её через plugin generations не
входят в текущий Core Management API.

Plugin, которому принадлежат cookie semantics, задаёт обычные и `HttpOnly`
cookies через собственный типизированный response contract. Server plugin
проверяет response целиком до отправки HTTP headers или WebSocket upgrade и
сохраняет атрибуты `Set-Cookie`; `pluginprotocol` остаётся транспортом
непрозрачных plugin-defined payloads и не определяет HTTP cookies.

Для входящих HTTP-запросов Server допускает route-scoped `requestCookieNames`
в настройках Server plugin. Server передаёт выбранной capability только cookies
с явно перечисленными именами, сохраняя порядок и повторения; по умолчанию список
пуст. Сырой заголовок `Cookie` и остальные cookies не пересылаются. Эта настройка
принадлежит конфигурации Server route, а не Core: отдельный Core Management API и
SQLite policy для cookies остаются вне текущего scope.

Для v2 требуется отдельно спроектировать per-instance/capability allow-list,
версионирование и атомарную активацию policy. До принятия и реализации такого
контракта не добавлять `/api/plugins/{id}/cookie-policies/*`, SQLite policy
tables или тесты, объявляющие функцию частью текущего Core scope.

`HttpOnly` запрещает JavaScript страницы читать cookie, но не мешает браузеру
посылать её в последующих подходящих запросах. Владельцем значения и lifecycle
cookie остаётся plugin; посредник не должен раскрывать cookie values в logs,
traces, audit, diagnostics или errors.
