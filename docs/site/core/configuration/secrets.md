# Секреты и grants

Core SQLite хранит только secret references; plaintext secret values и private
keys не попадают в DB, legacy YAML, service settings JSON, audit, error или
logs. Service SDK REST config pull отдаёт versioned config и opaque references.
Значение выдаёт scoped grant через защищённый Core REST endpoint; он ограничен
instance и settings revision либо одним конкретным call.

Сервис хранит активные application settings и разрешённые значения только в
памяти. После REST `Reload(generation)` он сам pull-ит указанную revision,
атомарно применяет settings/resources и отзывает старые config grants;
per-call grant очищается после использования. Service не читает app env/config
file.

Workload TLS private keys доставляются отдельным infrastructure credential
provider (локальный bootstrap pipe, remote PEM mount или SPIFFE API), а не
передаются через Service SDK REST. Management TLS identity, service workload
identity и Caddy ACME storage — разные trust domains. Секреты и credentials
редактируются до любого error/log/trace/audit boundary.

Peer transport grant metadata не копируется в Core docs; общий REST grant
lifecycle принадлежит Service SDK. Канон межсервисного обмена —
[wool](https://github.com/Liapoldus/wool). См.
[security boundaries](security).
