# Gateway v1 delivery checklist

## Acceptance gates

CI обязана выполнять полный TypeScript-набор, `go test -race ./...`,
`go vet ./...` и Docker smoke-проверку образа. Smoke-проверка подтверждает, что
минимальный distroless-образ запускает собранный бинарник; runtime-поведение
проверяется интеграционными тестами и не подменяется проверкой только сборки.
Legacy framing удалён; раздел ниже фиксирует переход на gRPC внутри plugin
protocol v1 и оставшиеся runtime-задачи.

Проверка 2026-09-23 после plugin-agnostic refactor: `make check` прошёл
(`go build ./...`, 59 TypeScript-файлов / 196 тестов, Docker architecture lint),
`go vet ./...` прошёл, VitePress build прошёл. Это не закрывает оставшуюся
semantic conformance проверку всех Gateway golden vectors против runtime.

## V1: cookie boundary и remote plugin mode (проектирование завершено)

Обе возможности ниже описаны как план Gateway v1 в канонических страницах
документации:
[`gateway/architecture/cookies`](https://liapoldus.github.io/gateway/architecture/cookies)
и
[`gateway/architecture/plugin-deployment`](https://liapoldus.github.io/gateway/architecture/plugin-deployment).
Runtime реализации ещё нет, кроме частичного response `Set-Cookie` passthrough
и обычного cookie forwarding через HTTP proxy. До начала соответствующей
реализации сначала добавить red TS-тесты в отдельные `tests/` каталоги и
зафиксировать wire/schema-контракт в `pluginprotocol`; не вносить plugin-specific
типы/ветви в Gateway core.

### Cookies

- [ ] В pluginprotocol закрепить typed request-cookie context и typed response
  cookie actions. В HTTP headers `Cookie` и `Set-Cookie` не дублировать.
- [ ] В core config расширить generic plugin capability context allow-list имен
  cookies; отсутствующий/пустой список не передаёт cookie. WAF-context всегда
  остаётся без cookie.
- [ ] Сохранять многострочный `Set-Cookie` без объединения; атомарно валидировать
  весь response до записи headers/body.
- [ ] Валидировать name/value, CRLF/control chars, `SameSite=None; Secure`,
  `__Host-`/`__Secure-` constraints, deletion/expiry и конфликт между typed
  actions и generic response headers.
- [ ] Задать versioned предел числа actions и сериализованного размера; ошибка
  при превышении до частичного ответа.
- [ ] Добавить end-to-end cookie round-trip для обычной и `HttpOnly` cookie,
  CORS credentials cases и тесты редактирования logs/traces/audit/errors.

### Remote plugin connection

- [ ] В pluginprotocol описать typed launch/connection modes, remote endpoint
  TLS credentials, fixed-address semantics и mTLS GrantBroker callback; не
  менять protobuf capability payloads или protocol namespace v1.
- [ ] В Gateway schema закрепить mutually exclusive tagged union `local` /
  `remote`; старые локальные конфиги остаются default/валидны, remote без TLS и
  неполные certificate references отвергаются.
- [ ] Реализовать TLS verification по CA + server SAN и взаимную проверку
  сертификата для межмашинного remote production mode; никаких insecure
  fallback/downgrade.
- [ ] Сохранить handshake, health, Call, Stream, capability allow-list,
  deadlines, backpressure, cancellation и redaction одинаковыми в обоих режимах.
- [ ] В remote mode не запускать process, не вызывать process Shutdown и не
  обещать RSS/process restart controls; disconnect управляется transport health,
  restart — внешней средой.
- [ ] Сделать remote GrantBroker доступным только по внутренней mTLS-сети;
  доказать тестом привязку redemption к живому Call/capability/purpose/domain и
  запрет выдачи по чужому/истёкшему handle.
- [ ] Добавить real gRPC TLS integration test, Compose/Kubernetes examples,
  certificate rotation/failure cases, reconnect и atomic config activation.
- [ ] Проверить Linux/macOS, `go vet ./...`, `go build ./...`, полный
  TypeScript suite, `make check`, pluginprotocol conformance и VitePress build.

Gateway runtime остаётся plugin-agnostic: конфигурация создаёт generic instance
только когда администратор его подключает. Core не содержит знаний о названиях
plugins, их capabilities/settings schema, image/ports или предметных cookies.

This is the execution order. Every checkbox is test-first: commit a failing
TypeScript test under `tests/` before the implementation that satisfies it.

## Historical audit findings (2026-09-22; закрыто последующими коммитами)

Первичный аудит ниже сохранён как исторический контекст. Все перечисленные
runtime-проблемы закрыты последующими реализациями и regression-тестами:
TLS/mTLS, L4 listeners, management API, secret resolution, service accounts,
metrics, gzip, SPA fallback, rewrite captures и route/plugin actions.
Архитектурный lint сейчас завершается `OK - No warnings found`.

Оставшийся acceptance-gap: семантическое исполнение всех 51 golden vectors
против runtime; сейчас проверяются manifest/checksums, структура и уникальность
векторов, а runtime-сценарии покрываются отдельными integration suites.

После включения OTLP metrics/tracing и access logging для data-plane и
Management API последняя проверка прошла `go build ./...`, `go vet ./...` и
Vitest: 51 TypeScript-файл / 182 теста. `make check` остановился на Docker
architecture lint с `error waiting for container: unexpected EOF` при запуске
amd64-образа на arm64-хосте; полный gate этим запуском не подтверждён. Предыдущий
успешный прогон lint/race/Linux build был до последних изменений логирования.
`npm run build` документации после обновления logging contract прошёл.
Во время Docker-сбоя оставалось около 118 MiB, после проверки документации —
около 349 MiB свободного места; системные данные не удалялись.
Поле `duration` в JSON access record закреплено как число секунд с дробной
частью; семантика добавлена в HTTP runtime contract и проверяется с задержанным
upstream.

Audit JSONL реализован для config reload: записи переживают перезапуск Gateway,
`/api/audit` читает их из `${registry.path}/audit/YYYY-MM-DD.jsonl`, применяет
90-дневный retention и не сериализует credential. Если `registry.path` не задан,
runtime использует каталог `registry` рядом с активным config; это значение
версионируется в `assets/contracts/registry-fields.yaml`. Actor общего
`management.staticToken` записывается как `static-token`; service account — по
ID. Успешные и неуспешные `config.reload` и `config.update` записываются вместе
с request ID и digest до/после; истёкшие файлы удаляются при чтении. Успешная
публикация записывает action `site_published`, успешный и неуспешный вызов rollback —
`site_rolled_back`; source path не записывается. Остаётся добавить audit для
остальных mutating Management API операций. E2E regression
проверяет append, restart, retention и отсутствие credential в ответе в
`tests/integration/audit-persistence.test.ts`.

Повторная проверка 2026-09-23 обнаружила, что TLS-профиль listener терялся:
в `assets/contracts/config-fields.yaml` отсутствовало runtime-сопоставление
`tls`. Маппинг восстановлен; новый child-process E2E подтверждает TLS
handshake, TCP+UDP на одном порту и настоящий HTTP/3 запрос. См. коммиты
`0aaaa97`, `4b6f1b4`, `a1d966c` и QUIC-limit пункт ниже.

Полный Vitest-прогон после обновления protocol однажды исчерпал temp-диск:
test helper оставлял по одному 22–25 MiB Gateway binary на Vitest worker.
Теперь `tests/support/gateway.ts` удаляет созданный им каталог в `afterAll`;
отдельный TS-тест проверяет этот lifecycle, повторный `make check` прошёл без
накопления артефактов.

Docker build использует workspace root, чтобы локальный `replace ../pluginprotocol`
в v1 `go.mod` разрешался внутри build stage. `make docker-smoke` создаёт временный
контекст только из двух репозиториев; CI checkout-ит core и sibling
pluginprotocol без публикации бинарных артефактов.

Verification was done against build `/tmp/liapoldus-gateway` with fixture
`/var/folders/qk/694xx2l56_l01zmd37h82szh0000gn/T/opencode/audit/`
(`gateway.yaml`, `www/` site + `site.yaml` manifest, batteries 1–4). Contract
references are
`liapoldus.github.io/public/spec/{gateway.schema.json,management.openapi.yaml,http-runtime.json,errors.json}`.

- `management.staticToken` is accepted as `env:`/`file:` (schema pattern) but
  **never resolved**: `collectManagement` (compile.go:718) stores the raw
  reference; `resolveSecrets` only processes the top-level `secrets:` mapping,
  so `serve` (serve.go:290) hands the literal `env:LIAPOLDUS_MGMT_TOKEN` to the
  API adapter → Bearer auth can never match the real token. `GET /api/status`
  only succeeds with `Authorization: Bearer env:...` as-is. Every `/api` and
  `/metrics` request with the intended token returns 401.
- `rewrite.replacement` using a capture group (`replacement: /sub/${1}.html`)
  is rejected by `validateScalar` (validator.go:545) as an undefined config
  variable → `ErrInvalidDocument` at compile. Plain literal replacements work
  and rewrite serves correctly at runtime. Needs a decision: exclude
  route/rewrite scalars from `validateVariables`, or scope variable
  substitution to a documented field allow-list.
- Multiple HTTP listeners are not served: `serve` (serve.go:21-25) `return`s on
  the first `IsHTTP` listener, so only the first listener binds; additional
  listeners are silently ignored.
- `management.serviceAccounts` are validated (schema: `id`+`role:
  platform-admin`+`keyHash: file:`) but unused: the adapter authenticates
  staticToken only. Accounts create/rotate/revoke work and match the API (keys
  `lpgw_<id>_<base64url>`, files `secrets/accounts/<id>.bcrypt` mode 0600,
  exit 4/5 on conflict/not-found) but keyHash is `.bcrypt` of the key, so keys
  can never be verified against a runtime account.
- TLS/mTLS/HTTPS not implemented: `network/http.go` (4 lines) and
  `security/tls.go` are stubs; `serve` only does `http.ListenAndServe`; listener
  `tls:` and `tlsProfiles:` compile but are never applied;
  `X-Forwarded-Proto` is hard-coded `http`. Non-loopback management with
  clientAuth is accepted by validation but unreachable at runtime. TCP/UDP
  listeners (`network/l4.go`) are a stub too.
- `GET /api/config` returns `yaml: ""` (empty) even though the server holds the
  compiled config; `PUT /api/config` and `/api/operations` and `/api/plugins`
  are unimplemented (404). `GET /metrics` is 404 (Prometheus export missing;
  no-token `GET /metrics` is 401-gated). `POST /healthz` is not method-gated
  (200). `GET /api/status` returns empty `listeners`/`upstreams` arrays.
- Gzip/compression not applied: `Accept-Encoding: gzip` on a static response
  returns identity (~13 bytes). SPA fallback only for requests whose `Accept`
  contains `text/html` (curl default `*/*` gets 404 on `/about`); manifest
  option works.
- site-level separator: manifest `headers` must be `headers.response.set` per
  `site.schema.json`; `headers.set` on the manifest is silently ignored.
  Canonical `config-fields.yaml` still lists `index`/`spa`/`redirects`/
  `headers`/`cache` as top-level site words in gateway.yaml, but the schema
  rejects them there (manifest-driven design) — docs wording needs alignment.
- Observed working: static index/ETag/304/206 range, traversal 404 for `/..`
  and `%2e%2e`, site redirect 301, rewrite literal, site cache
  `Cache-Control`, site response headers, proxy forwarding (request header
  set, X-Forwarded-For/Host preserved), POST body echo, no-match 404,
  /healthz 200, /api/status|config|validate|audit|admin-surfaces 200,
  config validate/explain/print/diff, accounts CRUD + exit codes.

## Decisions and contract corrections

- Schema bug: `pathMatcher` used `oneOf [$ref stringMatcher, object exact|prefix|regex]`;
  `{prefix: /}` matched both branches. Fixed canonical
  `liapoldus.github.io/public/spec/gateway.schema.json` and the mirrored copy
  `assets/contracts/gateway.schema.json`: `pathMatcher` is now
  `oneOf [string ^/, array<minItems 1 ^/>, object exact|prefix|regex ^/]`.
  TODO.md + docs site were updated after a User decision (fix canonical + copy).
- Schema bug: `proxyTarget.host` used
  `oneOf [{const preserve}, {const upstream}, {type string}]`;
  `preserve`/`upstream` matched both the `const` branch and `type: string`, so
  `oneOf` (exactly one) rejected the documented `host: preserve` and
  `host: upstream` configs as `config_invalid`. Fixed canonical
  `liapoldus.github.io/public/spec/gateway.schema.json` and the mirrored copy
  `assets/contracts/gateway.schema.json`: the string branch is now
  `{ "type": "string", "not": { "enum": ["preserve", "upstream"] } }` with
  `default: preserve`. TODO.md + docs site were updated after a User decision
  (fix canonical + copy); regression test in `tests/integration/proxy.test.ts`
  asserts `host: preserve` and `host: upstream` validate clean (exit 0).
- Refactor-first decision: existing Go was refactored to the "no domain string
  literals" rule before new increments — CLI/registry/network vocabulary moved
  into `assets/contracts/{cli-fields,registry-fields,config-fields}.yaml`
  loaded through the embedded `assets` package.
- Parallel increment decision: `serve`+directory static runtime and the CLI
  `config print|format|explain|diff` (plus secret redaction and semantic
  validation) are being delivered in parallel by two subagents.
- Network arch-lint fix: YAML parsing moved from `network` to `config`
  (`config.CompileGateway` → `models.CompiledGraph`); `network.Serve` consumes
  compiled domain models only. The single internal invariant message "no http
  listener" remains in `network` (unreachable given compile guarantees).
- `config print` merge semantics decision: the effective document merges root
  and includes with later documents winning per key (the whole value is
  replaced, matching CompileGateway site override and YAML duplicate-key
  behaviour), drops the `includes` key, resolves `${var}` substitutions on all
  scalars except the `secrets` section, and renders secrets only as
  `env:`/`file:` references so values can never leak.
- Path matcher semantics decision: `when.path` is compiled from the canonical
  `pathMatcher` forms — bare string and array of strings are prefix matchers
  (array = any-of), and the object form supports `prefix`, `exact`, and
  `regex` (precedence in that order). Regex matchers are anchored at compile
  time (`^(?:pattern)$`, whole-path RE2 match) and validated during
  `CompileGateway`; an invalid regex fails configuration compilation. A regex
  selects the site only — it never influences file resolution, which stays
  bounded by `site.Root` via the existing traversal guard. An absent
  `when.path` remains a catch-all route.
- `config explain` semantics decision: the report is built from the compiled
  graph (supervise + buildCompiled) plus the raw source documents. Route → site
  references are validated in `CompileGateway` via `validateReferences` — a
  route targeting an undefined site is a hard `config_invalid` failure (exit
  3), for explain and serve alike. Non-fatal issues are reported only:
  `unused` (a site never targeted by a route) and `overridden` (a site name
  defined in more than one configuration document; counts are taken from the
  physical source files, i.e. the root file plus each include, so a site that
  only ever exists in one document is not a false positive). Listener types
  and addresses are read from the raw documents; site entries report the
  resolved index.
- Runtime word binding bug fixed: `yaml.v3` lowercases untagged struct field
  names, so `runtimeWords.Site.IndexDefault` and `ManifestFileName` never
  bound to the camelCase contract keys `indexDefault` / `manifestFileName`
  and the compiled site index defaulted to empty. Added the missing yaml tags
  in `validator.go`; `config explain` site entries now surface the resolved
  `index.html` default.
- `config diff` semantics decision: the two positionals are both optional —
  each missing one falls back to the config discovery chain (and equals the
  first when neither is given). The comparison is over the effective document
  used by `config print`: includes merged (later wins), the `includes` key
  dropped, `${var}` substitutions already resolved, and secrets compared only
  as `env:`/`file:` references so values can never appear in output. The
  `listeners` and `sites` sections diff per entry (`name` set); every other
  section is compared as a whole (section-level change with no `name`). Text
  output echoes the real section words (`listeners`/`sites`/...), so entry
  lines read `changed listeners main`, section lines read `changed section
  variables`. The command exits 0 whether or not differences are found.
- Directory-static hardening decisions: directories are never served as
  listings. A request path that maps to a directory is resolved to that
  directory's index file; a missing index (or a missing/empty path) is a 404.
  Request paths containing a `..` element are rejected with 404 at the
  gateway (this overrides `http.ServeFile`'s own `400 invalid URL path`,
  giving a uniform 404). An empty request-target is rejected by `net/http`
  itself with 400 before the handler runs — the gateway's `404` for
  `request.URL.Path == ""` is backstop-only and unreachable over `HTTP/1.1`.
  Symbolic links are still resolved (followed) by `http.ServeFile`; real
  symlink policy for `directory` sources remains part of the
  "traversal/symlink protection" item below.
- Release-source serving decision: a site with `type: release` serves only
  files from its target revision — the `current` symlink under
  `<root>/sites/<slug>/current` (releases are never read by revision anywhere
  else). Serving is lazy: before the first publish the `current` target does
  not exist, and a release site therefore serves nothing (404). The same
  traversal guard and `..` rejection as directory sources apply. The registry
  path resolves relative to the configuration file directory, mirrors the
  site-root rule of directory sources. Publish rewrites `previous`/`current`
  atomically and a failed publish leaves both pointers unchanged.
- Management publish E2E is backed by the filesystem registry and now matches
  `PublishRequest.additionalProperties: false`: unknown JSON properties and
  trailing JSON values return 400. Publish and rollback require
  `expectedCurrentRevision`; the filesystem registry compares it and switches
  release pointers under the same site lock. Stale requests return typed
  `release_revision_conflict` details without changing pointers. Publish and
  rollback retries are idempotent for the 24 h in-process window, and operation
  responses expose a distinct operation ID plus the resulting release revision.
  `GET /api/sites` now reports sorted live current/previous revisions and never
  returns directory source paths. Persistence across Gateway restart remains an
  explicit product decision; the docs specify a 24 h retry window but not
  restart behavior. The
  Management API rollback error and CLI `rollback-missing` are covered by
  child-process E2E; CLI publish/release response and directory-source rejection
  now have child-process coverage. Durable idempotency across
  process restart remain incomplete. A repeated publish/rollback reuses its
  original operation response and `X-Request-ID` as required by `sameResponse`.
  Site state is `ready` for a valid configured site and `invalid` when a release
  pointer cannot be inspected. CLI publish does
  not yet accept `--idempotency-key`; implement after deciding whether the
  documented 24 h deduplication must survive process restart. `site current`
  and `site previous` now read and validate the release pointers and return
  `null` before first publish; `site versions` output shape is not specified
  beyond listing retained revisions, so that command remains pending contract
  clarification. Child-process CLI E2E verifies third publish retains exactly
  current/previous and removes the oldest release. Pointer inspection rejects
  symlinked site/release directories and targets outside the direct releases
  directory; E2E confirms the external path is not exposed. Fixed Management
  API endpoint paths are now loaded from `management-fields.yaml`; a TS
  architecture test prevents reintroducing those URL literals in the adapter.
- Snapshot persistence decision: the runtime snapshot store gained a
  filesystem adapter (`FilesystemSnapshotStore`) that mirrors the in-memory
  store's prepared/active/drained contract. Snapshots are serialized as JSON
  via stdlib `encoding/json` on the domain models (storage imports only
  domain + stdlib, so no yaml/config). File names are contract words in the
  new `snapshot-fields.yaml` (`active`, `preparedPrefix`, `drained`) reached
  through `config.LoadSnapshotLayout`. A prepare writes a `prepared-*` file;
  activate matches a prepared file by value, then swaps the `active` file via
  temp-file write + atomic rename and returns the previous active; drain
  writes the `drained` file the same way. Any failed prepare/activate/drain
  leaves the active pointer unchanged — a failed activate keeps the matched
  prepared file so it remains retryable. Because `Secret.Value` is
  `json:"-"`, secret values can never be persisted; the known limitation is
  that `PathMatcher.Regex` does not round-trip through JSON (matcher needs a
  serializable form before full graphs can be persisted).
- Route action semantics decision (derived from `public/spec/http-runtime.json`
  pipeline and `server-blocks.md`, no doc rule existed so recorded here):
  per route, at most one terminal target (`site`, `proxy`, `redirect`) — this
  is enforced as a hard `config_invalid` at compile time (separate increment).
  Transform actions are ordered by the runtime pipeline: `request-headers` →
  `rewrite` → terminal → `response-headers`. Within one side of a
  `headers` action the operations apply in fixed order set → setIfAbsent →
  delete, so a `delete` wins over any other entry on the same header name.
  Gateway-owned forwarded headers (`Host`, `X-Forwarded-*`) are written by
  the proxy director at the terminal stage, so `headers.request` can never
  override them (consistent with the decision that the Gateway owns
  forwarded headers). `headers.response` merges site-then-route, route wins.
  A `redirect` terminal builds an absolute `Location`: scheme =
  `redirect.scheme` else request scheme (http, the gateway has no TLS yet),
  host = `redirect.host` else the request Host, path = `redirect.path` else
  the rewritten request path, query appended only when `preserveQuery`
  (default true) and a query is present; status defaults to 308 and accepts
  301/302/307/308. A `rewrite` action compiles its `regex` (RE2) and applies
  it exactly once before the terminal to the request path only (query is
  preserved untouched, and the rewritten path is what a `redirect` without
  its own `path` would use); `replacement` expands `$N` capture groups.
- Route terminal exclusivity decision (derived from `server-blocks.md`
  `одно route action — ровно один terminal target`): a compile-time check in
  `CompileGateway` rejects any route whose `then` carries more than one
  terminal action as `config_invalid` (exit 3). The runtime now compiles the
  `site`, `proxy`, `redirect`, `deny` and `plugin` route actions; plugin dispatch
  still requires a supervisor-provided capability map. Generic WAF plugin
  actions dispatch an arbitrary configured capability and apply its typed
  continuation/HTTP-response result. WAF поддерживает path, method, прямой
  source-IP/CIDR, header, query и Geo/ASN country/city/ASN matcher-ы с действиями
  allow/deny/limit. MMDB-reader проверяется перед reload; новая конфигурация и
  WAF/MMDB-поколение меняются только после успешной подготовки, при ошибке
  остаются прежние digest и трафик. HTTP requests теперь читают один
  подготовленный generation: при reload уже привязанный listener атомарно
  начинает использовать новые routes, sites, upstreams, rate limits,
  auth/WAF policies и MMDB. Смена/добавление/удаление listeners и их адресов,
  TLS-профилей, L4 rules и plugin processes пока не входит в generation swap;
  нужны Listener Manager, подготовка этих ресурсов и bounded drain старого
  поколения, включая согласование data-plane и Management API revision.
  Ошибка provider теперь возвращается как RFC Problem с кодом
  `waf_provider_unavailable` и покрыта integration-тестом. Geo/ASN положительное
  совпадение уже проверяется в `tests/integration/actions.test.ts`; остаётся
  успешный MMDB replacement integration-тест с изменившимся содержимым базы,
  matcher `connectionAge`, request-size matcher для L4, согласование HTTP route
  matcher-ов. HTTP WAF `requestSize` реализован как фактический
  размер тела после снятия transfer framing; chunked учитывается, тело
  сохраняется для downstream, а `listener.limits.bodyBytes` ограничивает
  чтение до диспетчеризации. Семантика описана в
  `liapoldus.github.io/gateway/configuration/security.md` и
  `liapoldus.github.io/public/spec/security-runtime.json`. Recursive WAF
  `all`/`any`/`not` теперь компилируются и выполняются;
  provider failure вычисляется как unknown и не инвертируется через `not`.
  Семантика композиции описана в Gateway security-документации и
  `security-runtime.json`.
  CORS preflight теперь проходит terminal только при разрешённых Origin и
  `Access-Control-Request-Method`; несовпадающий запрос продолжается обычным
  маршрутом и покрыт TS integration-тестом.
- Schema bug: `$defs.tlsProfile.certificates.items` previously misplaced
  `additionalProperties: false` next to `oneOf`, rejecting valid certificate
  entries. The schema was corrected and verified with the Go JSON Schema
  validator. Gateway v1 now accepts explicit `cert`+`key` material only;
  certificate issuance/provider settings were removed from the Gateway contract
  so they can be owned by a separately connected integration. `clientAuth:
  {mode: require}` remains Gateway transport security.
- Runtime word binding bug (round 2): `yaml.v3` lowercases untagged struct
  field names, so the camelCase runtime words never bound and management
  semantics mis-fired: `runtimeWords.SiteRedirect`/`SiteCache`/`TLSProfile`/
  `ClientAuth` (and `SiteCache.MaxAge`) matched no YAML keys, leaving
  `words.ClientAuth.Require` empty so the mTLS check
  `profile.ClientAuth.Mode != "" == ""` never tripped and a remote listener
  with a non-mTLS profile slipped to the account-required error instead of
  `management_mtls_required`; TLS certificate collection (`collectTLSProfiles`)
  read nothing. Added the missing yaml tags in `validator.go`. Separately, the
  `runtime.http/directory/release` value words (`words.HTTP`, `words.Directory`,
  `words.Release`) had no contract entries at all, so `listener.IsHTTP` stayed
  false and `source.type: directory|release` never set `site.Source`/`Root`
  (directory sites lost their root and release/directory detection failed).
  Added `http:/directory:/release:` keys under `runtime:` in
  `assets/contracts/config-fields.yaml`. After the fix `config explain` and the
  serve pipeline resolve source kinds and roots, and `missing-mtls` returns
  `management_mtls_required`.
- WIP regression caught before commit: uncommitted management-semantics work
  replaced `validateReferences` and dropped its undefined-site/undefined-upstream
  membership checks, silently letting `config explain` and `serve` accept a
  route targeting an undefined site/upstream (regression against the committed
  `config_explain` red/green). Restored the checks at the top of
  `validateReferences` using the existing `ErrUndefinedSite`/`ErrUndefinedUpstream`
  sentinels (bare errors map to the canonical `config_invalid`, exit 3 via
  `configValidationFailure`).
- Contract adapter arch-lint rule: the `go:embed` contract adapter lives in
  the module-root `core` package (`contractassets.go`, moved from
  `assets/embed.go`). `.go-arch-lint.yml` now declares a `contractAdapter`
  component (`in: [.]`) and lets `infrastructureConfig` depend on it.

## Accounts / bearer auth increment — нормативные правила v1

`gateway accounts create|rotate|revoke` vocabulary is already contracted in
`assets/contracts/cli-fields.yaml` (`keyPrefix: lpgw_`, `keyBytes: 32`,
`hashCost: 12`, `hashExtension: .bcrypt`, `secretsDir`, `accountsDir`,
`keyHashWord`, `saveMessage`/`rotateMessage`/`revokedMessage`,
`roleRequired`/`roleInvalid`/`accountIDRequired`/`accountNotFound`/
`hashInvalid`, exits ok/arguments/...). Решения ниже обязательны для v1:

1. Секрет содержит 32 криптографически случайных байта, кодированных
   `base64url` без padding: `lpgw_<account-id>_<payload>`. Hex и стандартный
   base64 недопустимы.
2. Повторный `create` завершается кодом `4` (`account_conflict`), а
   `rotate`/`revoke` неизвестного аккаунта — кодом `5` (`account_not_found`).
3. CLI создаёт `secrets/accounts`, пишет bcrypt с cost `12` через временный
   файл в том же каталоге и `rename`, выставляя режим `0600`. `create` не
   перезаписывает существующий файл; `rotate` требует существующий аккаунт.
4. CLI никогда не переписывает `gateway.yaml`: он печатает ключ ровно один
   раз и выводит оператору готовый фрагмент `keyHash: file:...`.

## 0. Foundation

- [X] Add the Go module, `cmd/gateway`, build targets, short README, Dockerfile
  and GitHub Actions for Ubuntu and macOS.
- [X] Add `tests/package.json`, Vitest + tsx setup, process lifecycle helpers,
  temporary registry/config fixtures and contract downloader.
- [X] Add red tests for CLI config discovery, missing config, invalid config,
  redacted config printing and validation without runtime mutation.
- [X] Implement the smallest CLI/config compiler surface that makes those
  tests pass.

## 1. Configuration and snapshots

- [X] YAML decoding; includes/globs/cycle detection; variables and `env:` /
  `file:` secret references with redaction.
- [X] JSON Schema plus semantic validation: named-resource references, regex,
  route terminal-action rules and management listener security rules.
- [X] Immutable compiled graph, SHA-256 digest/revision, prepare/swap/drain and
  rollback-on-failure semantics.
- [X] CLI `serve`, `config validate|path|print|format|explain|diff`, typed
  diagnostics, exit codes and RFC 9457 problems.

## 2. HTTP and site registry

- [X] HTTP listeners, ordered routes/matchers, rewrites, headers, redirects,
  policy pipeline and access telemetry.
- [X] Directory and release static sources: `site.yaml`, traversal/symlink
  protection, SPA conditions, locales, ETag, conditional and range requests.
- [X] Reverse proxy: health checks, DNS/static targets, balancing, retries,
  forwarded headers and WebSocket upgrade.
- [X] Filesystem registry: staged immutable publish, in-process site
  serialization, cross-process advisory lock, active-lock rejection,
  dead-owner/expired-lease recovery, recovery audit, atomic current/previous
  changes, retention and rollback. TS child-process E2E verifies active and
  stale metadata, unchanged pointers on conflict, recovery audit, and an actual
  OS lock held by a separate process.

## 3. Management and observability

- [X] Local Bearer static token/service accounts, bcrypt key lifecycle and
  remote TLS + mTLS enforcement.
- [X] All documented Management API resources, cursor pagination, operations,
  audit records, config `If-Match` and secret-safe responses.
- [X] Prometheus exposition and periodic OTLP/HTTP protobuf metrics export.
  Collector failures leave HTTP requests unaffected, increment the local
  `liapoldus_otel_export_failures_total` counter and emit a generic JSON warning
  without logging the endpoint or transport error.
- [X] Emit configured JSON access records for data-plane and Management API
  responses to stdout/stderr, excluding query strings and request headers,
  counting response bytes after content encoding, and correlating request IDs.
- [X] Finalize the JSON access-record `duration` representation as fractional
  seconds measured from Gateway handler entry until its return after response
  writes. The child-process test uses a delayed upstream to distinguish seconds
  from milliseconds and also verifies structured fields, IDs, wire bytes, and
  exclusion of query/header secrets.
- [X] Route OTLP metrics/tracing exporter warnings to `logging.application`
  sinks, defaulting to `stderr`; an E2E test selects `stdout` and verifies the
  message is routed there without exposing the collector address. Empty sink
  lists are rejected by the configuration schema.
- [X] Export HTTP server spans over OTLP/HTTP protobuf, extract/inject W3C Trace
  Context, propagate child context to HTTP upstreams, and apply all configured
  `parent-based`, `always-on`, and `always-off` sampling modes. Spans omit query,
  headers, body and exporter endpoint. Process-level TS tests verify sampled and
  unsampled parents and upstream span ID propagation.

## 4. Security, TLS and L4

- [X] TLS profiles/storage/reload, certificate selection and mTLS runtime
  operation lifecycle.
- [X] Bind route auth policies to arbitrary configured plugin capabilities via
  the common HTTP capability dispatcher. Core does not interpret tokens,
  claims, sessions, or provider-specific identity settings.
- [X] TCP and UDP listener/rule engines, upstream
  relays, flow limits and graceful shutdown.

## 5. Plugin integration

- [X] Publish and pin `github.com/Liapoldus/pluginprotocol` v1.0.0 from the
  sibling `pluginprotocol` repository.
- [X] Gateway plugin supervisor: loopback launch, manifest/health/config.apply
  handshake, restart/backoff, logs, RSS/call limits and scoped grants.
- [X] HTTP capability, TCP stream and UDP flow dispatch using test plugin
  processes in `tests/fixtures/` only.

## 6. v1 acceptance and ownership transfer

- WAF limit action is executable through named token buckets. The WAF section
  root word is now loaded from `assets/contracts/config-fields.yaml`; unknown
  WAF/rate-limit references fail config validation instead of failing open at
  request time. The TS integration case covers first-request pass, exhaustion,
  `Retry-After`, and confirms that the denied request never reaches upstream.
  Additional integration cases verify that method, source-IP/CIDR, header and
  query conditions gate policy actions with AND semantics and the socket peer
  address.

- [ ] Execute all 51 current documentation golden vectors semantically against
  runtime on macOS/Linux. `http3-bind` is now exercised end-to-end, including
  TLS, shared TCP/UDP port and an HTTP/3 request; `audit-retention` is verified
  through the authenticated Management API; `publish-idempotent` is verified
  through a real child-process publish, retry, body/key conflict, serving and
  audit check; `rollback-missing` is verified via the documented CLI command;
  `directory-source-publish` is verified via the CLI without registry mutation;
  `release-retention` is verified by three real child-process Management API
  publishes, current/previous pointers, removal of the oldest release, and
  exactly three `site_published` audit records. CLI release retention is also
  independently covered. The `spa-fallback` and `spa-asset-miss` vectors now
  run against a real Gateway child process; missing assets return the
  catalogued `route_not_found` problem. `static-etag-not-modified`,
  `static-range-single`, and `static-range-unsatisfiable` now use catalog values
  in real Gateway child-process tests. `static-path-traversal` is sent as a raw
  encoded HTTP path and returns the catalogued problem. `publish-stale-release-revision`,
  release revision CAS, and concurrent publish conflict are verified against a
  real Gateway child process. `locale-prefixed` and `locale-unprefixed` are
  exercised through actual filesystem resolution; `locale-invalid-default` is
  bound to semantic config validation. Unprefixed paths use `defaultLocale`
  without redirect or `Accept-Language` negotiation. `compression-qvalue` is
  verified against a 1024-byte static response and actual response headers.
  `cors-preflight` checks the catalogued status/header and proves the upstream
  terminal was not called. `rate-limit` now verifies the RFC 9457 error body,
  `Retry-After`, and that the exhausted request never reaches upstream.
  `body-limit` now sends the documented first byte beyond 10 MiB to a real
  Gateway process and checks the catalogued RFC 9457 code and status.
  `waf-limit` is now asserted alongside `rate-limit` in the exhausted named
  token-bucket integration case, including the catalogued status and Problem
  code before proxy dispatch.
  `cli-config-discovery` now verifies both the resolved `gateway.yaml` path
  and the exact `LIAPOLDUS_CONFIG_DIR` source value; the obsolete generic
  `environment-directory` label was removed from the CLI contract assets.
  `cli-config-missing` now binds the no-source case to the vector's exit code
  and error code.
  `release-symlink` now publishes a valid baseline, attempts a real Management
  API publish containing a symlink escape, and verifies `release_invalid`, an
  unchanged current pointer, and no copied target or source path in the reply.
  `telemetry-export-failure` and `metrics-exporter-isolation` now use a real
  process to prove HTTP remains healthy when an OTLP collector is unavailable,
  failure is locally counted/logged without endpoint details, and a healthy
  collector receives non-empty OTLP/HTTP protobuf at `/v1/metrics`.
  `geo-provider-failure-deny` verifies fail-closed status and catalogued code
  when the MMDB cannot be opened. `proxy-websocket` now passes through a real
  WebSocket echo upgrade. `header-limit` now enforces the configured HTTP/1
  header threshold and returns the catalogued RFC 9457 `header_too_large`
  response in a real child-process test. `publish-lock-active`
  now verifies `409 publish_in_progress`, stable pointers and both metadata-held
  and OS-held locks; `publish-lock-recovery` verifies a stale PID is recovered,
  the publish proceeds and `publish_lock_recovered` is audited.
  `directory-manifest-reload` now passes a real Gateway child-process test:
  changing only directory `site.yaml` preserves the old manifest until
  `/api/reload`, then new requests use the recompiled snapshot.
  `proxy-forwarding` now verifies a real HTTPS child-process request, including
  spoofed forwarding-header replacement, hop-by-hop stripping, upstream Host,
  scheme and listener-port propagation. `udp-upstream-protocol` now verifies an
  opaque UDP datagram reaches a real upstream and returns byte-for-byte intact.
  `plugin-stream-tcp-bytes` now sends the documented binary payload through a
  real TCP listener into the child plugin and verifies exact bytes.
  `mtls-required` verifies catalogued HTTP 401 on public HTTPS and the
  Management API without a client certificate, and confirms valid certificates
  continue through both listeners. `plugin-stream-udp-datagram` now sends the
  vector's exact bytes and a second independent datagram through real Gateway
  child-process plugin streams, checking raw-byte preservation in both.
  `tcp-plugin-protocol` now drives the vector's exact bytes through the
  `peer.session` capability over a child-process Gateway TCP listener. The
  `plugin-memory-limit` vector now drives the fixture's RSS and configured limit
  exactly; 12 vectors remain semantically incomplete.
- [X] Enforce all `listener.limits.quic` fields for HTTP/3. `maxConnections`
  caps simultaneous established QUIC connections, `maxStreams` caps concurrent
  incoming bidirectional streams, `idleTimeout` closes an inactive HTTP/3
  connection at the configured duration, and `maxPacketBytes` caps outgoing
  UDP datagrams at the configured byte limit through the maintained local
  quic-go v0.62.0 patch. TS child-process tests exercise each behavior. Generic
  listener `connections`/`bytesPerSecond` limits remain independent.
- [X] Resolve `plugin-startup-order` timeout mismatch: add independent
  `plugins.<instance>.limits.startTimeout` (default `10s`) for endpoint dialing
  and the complete startup handshake; keep `limits.timeout` (default `5s`) for
  Calls. Schema, config compiler, runtime and protocol/configuration docs agree;
  a delayed child-process fixture verifies that startup expires independently.
- [X] Resolve the invalid-client-certificate outcome mismatch: missing client
  certificate returns the catalogued HTTP `401 mtls_required`; an invalid
  presented certificate fails TLS verification before HTTP and receives no HTTP
  response. This is recorded in `public/spec/security-runtime.json` and the
  Gateway security/authentication pages.
- [X] Pass race, malformed-input, shutdown/recovery and no-secret regression
  suites on the current CI host.
- [X] Build and smoke-test the Docker image; ensure GitHub Actions reports all
  test and contract failures.
- [X] Generate Gateway contracts in `core` and publish the versioned GitHub
  Release asset `gateway-v1.0.1`; the documentation now links to the release
  and its downloadable archive.

## Plugin protocol: gRPC transport migration (блокирует v1 readiness)

Core переходит с pluginprotocol v1.0.0 TCP framing на gRPC/HTTP/2 по
TCP-loopback. Целевой контракт описан в
`liapoldus.github.io/gateway/architecture/protocol.md` и в README
`pluginprotocol`. По решению пользователя migration breaking, но остаётся
внутри protocol/module v1: сохраняются import path и protobuf namespace
`liapoldus.plugin.v1`; старые v1.0.0 framing plugins несовместимы и dual-stack
не будет. Следующая planned публикация — pluginprotocol v1.1.0. Это намеренное
исключение из обычного semantic-versioning ожидания и должно быть явно
отмечено.

- [X] В `pluginprotocol` заменить framing/session API на generated gRPC service;
  добавить typed control RPCs `Manifest`, `ConfigSchema`, `ConfigApply`,
  `Shutdown`, стандартный `grpc.health.v1`, unary `Call` и bidi `Stream`.
- [X] Сохранить JSON contracts и Gateway dispatch types `HTTPRequest`,
  `L4Request`, `RequestContext`, grants и redaction; не
  переносить Gateway process supervision/policy в протокол.
- [X] Зафиксировать typed L4 lifecycle в `pluginprotocol` v1: TCP использует
  один `Stream` на connection, UDP — один `Stream` на datagram; Open несёт
  ограниченный JSON context, Data — raw bytes и направление, Close — typed
  outcome. Единственный источник — `pluginprotocol/proto/.../service.proto` и
  `contracts/protocol/v1/stream-open-context.schema.json`; docs больше не
  дублируют старые `Frame/Envelope` encoding.
- [X] Перевести L4 dispatch на typed bidi Stream без изменения public JSON
  `L4Request`/`L4Response`: TCP открывает один stream на connection, UDP — один
  lifecycle на datagram; cancellation закрывает клиентский socket/stream,
  ответы проверяются по direction/Close code, message size и RSS limits
  сохраняются. Child-process E2E проверяет TCP dispatch и UDP raw bytes.
- [X] Включить standard gRPC reflection на plugin loopback endpoint для
  `grpcurl`; Constructor ↔ Gateway control plane оставить REST.
- [X] Добавлять отдельные TS red-test commits перед каждым protocol/core
  implementation increment. Никаких Go `*_test.go`; generated TS stubs —
  только в `tests/`, без публикуемого npm package.
- [ ] Проверить protobuf descriptor conformance, versioned JSON schema
  examples, malformed/oversized payloads, deadlines, cancellation, concurrency,
  обе стороны bidi stream, bounded backpressure, close race и restart.
  Независимое выполнение четырёх unary-вызовов покрыто реальным protocol
  child-process TS-тестом; cancellation/close-race stress и остальные пробелы
  см. `pluginprotocol/TODO.md`.
- [X] Заменить framing wire-hex vectors protocol suite на proto/JSON
  conformance. Оставить Gateway golden vectors только для observable Gateway
  behavior; синхронно обновить source docs, `contracts/v1/manifest.json` и
  contract checksum.
- [X] Добавить TypeScript integration suite с реальным child-process gRPC
  plugin: handshake, health, unary Call, HTTP/TCP/UDP dispatch, L4 bidi Stream,
  secret redaction и штатный shutdown.
- [X] Проверить grpcurl reflection и health на реальном gRPC fixture;
  `list`/`describe` возвращают health, reflection и `PluginService` v1.
- [X] Проверить restart/backoff после падения дочернего процесса через child-process E2E.
- [X] Удалить production `framing/`, `session/`, probe и generated
  framing-only code после replacement suite green.
- [X] Добавить в `pluginprotocol` generated Go gRPC stubs, test-only TypeScript
  gRPC stubs и CI `make check-generated` stale-output gate.
- [X] Добавить protobuf/JSON conformance CI; переключить core protocol adapter
  на v1 gRPC transport API с local sibling-module replace.
- [X] Подключить process supervisor, startup handshake, loopback endpoint
  handoff по pluginprotocol launch contract, settings/config apply и generic
  HTTP/L4 dispatch к configured plugin instances в `core`.
- [X] Применять `limits.calls` как максимум одновременных plugin RPC на instance;
  timeout включает ожидание свободного слота и RPC.
- [X] Подключить `restart.enabled/backoff/maxBackoff` и health probe (5 s,
  restart после 3 последовательных failures) к plugin process runtime.
- [X] Реализовать scoped secret grants для HTTP plugin routes: compiler
  разрешает имена только через `plugins.<instance>.grants.secrets` и проверяет
  наличие исходного secret; `plugin.context.secrets` выбирает grant на один
  вызов. Gateway создаёт отдельный loopback GrantBroker на instance, передаёт
  plugin только opaque handles в typed `CallRequest.grants`, а raw secret
  возвращает только в `RedeemGrantResponse`. Handle связывается с capability,
  purpose и domain scope; wrong-purpose/domain, чужая capability и отозванный
  handle отклоняются. Secret/handle не попадают в JSON, ошибки, logs или HTTP
  response. Протоколный contract — `pluginprotocol/proto/.../grant.proto`.
- [X] Реализовать plugin-agnostic WAF capability boundary: правило может
  вызвать любую capability, объявленную подключённым instance; в контекст
  входят ограниченные request facts без Authorization/Cookie/proxy credentials.
  Ответ — ровно одно из `continue` или типизированного HTTP response action,
  включая отдельный список `Set-Cookie` actions. Gateway не владеет challenge,
  token, provider, verification endpoint, cookie lifecycle или identity claims.
- [ ] Проверить полный security lifecycle на стороне соответствующего plugin
  repository; его capability/settings schemas и provider-specific behavior не
  входят в контракты или runtime-код `core`.
- [ ] Реализовать отдельно scoped storage operation model/handles; secret
  redemption RPC не предоставляет plugin файловые пути или storage access.
- [ ] Развивать конкретные plugin products в отдельных plugin repositories.
  `core` принимает их только как произвольные подключённые capabilities и не
  содержит перечня продуктов, capability names или их configuration schema.
- [X] Проверить core child-process E2E с capability-binding и новые L4 TCP/UDP
  Stream acceptance suites. Последний полный `make check` прошёл: 57 файлов /
  191 тест, `go build ./...` и architecture lint `OK - No warnings found`;
  дополнительно прошёл `go vet ./...`. Protocol `make check` прошёл:
  24 TS-теста, generated-code gate, `go vet`, `go test -race ./...` и `go build`.
- [X] Настроить protocol CI matrix для macOS и Linux.
- [X] Acceptance: `go vet ./...`, `go build ./...`, core `make check`,
  `go test -race ./...` и полный pluginprotocol TypeScript suite проходят.
  На 2026-09-23: core —
  57 TypeScript-файлов / 191 тест, protocol — 24 TypeScript-теста.
  Architecture lint завершился `OK - No warnings found` (на macOS ARM64
  используется Docker amd64-образ). Реальный core child-process acceptance
  покрывает handshake/health, unary Call, HTTP/TCP/UDP dispatch, L4 bidi Stream,
  redaction и shutdown;
  call concurrency, deadline → `504 plugin_timeout`, RSS breach →
  `503 resource_exhausted` и restart после child exit. HTTP/3 проходит реальный
  child-process запрос через QUIC. `grpcurl list/describe` проверен вручную.
  Остаются scoped storage operation model, завершение forms-db и запуск
  остальных plugins, durable idempotency storage (decision requested), аудит
  остальных mutating Management API операций и
  семантический прогон оставшихся 15 Gateway vectors.
- [X] Применять `limits.memory` как RSS limit процесса на macOS и Linux: RSS
  опрашивается раз в секунду и после capability Call; breach останавливает
  plugin, возвращает HTTP `503 resource_exhausted`, а при включённом restart
  запускает его снова согласно backoff. Default — `256MiB` из contract assets;
  TS child-process integration проверяет breach на реальном gRPC plugin.
