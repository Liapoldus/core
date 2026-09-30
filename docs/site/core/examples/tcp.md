# TCP relay — v2

Публичный TCP relay и Caddy-L4 не входят в Core v1. Страница сохранена как
указатель на отложенную функцию; settings schema, реализацию и acceptance
нужно проектировать отдельно в рамках v2.

Внутренний TCP carrier в `pluginprotocol` — отдельная plugin↔plugin сеть и не
является публичным relay.
