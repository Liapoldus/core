# TCP relay — v2

Публичный TCP relay и Caddy-L4 не входят в Core v2. Страница сохранена как
указатель на отложенную функцию; settings schema, реализацию и acceptance
нужно проектировать отдельно на следующем этапе.

Внутренний TCP carrier в `pluginprotocol` — отдельная plugin↔plugin сеть и не
является публичным relay.
