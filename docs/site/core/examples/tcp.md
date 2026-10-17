# TCP relay — v2

Публичный TCP relay и Caddy-L4 не входят в Core v2. Страница сохранена как
указатель на отложенную функцию; settings schema, реализацию и acceptance
нужно проектировать отдельно на следующем этапе.

Внутренний TCP carrier в `wool` — отдельная service↔service сеть и не
является публичным relay.
