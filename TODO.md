# Gateway v1 delivery checklist

This is the execution order. Every checkbox is test-first: commit a failing
TypeScript test under `tests/` before the implementation that satisfies it.

## Decisions and contract corrections

- Schema bug: `pathMatcher` used `oneOf [$ref stringMatcher, object exact|prefix|regex]`;
  `{prefix: /}` matched both branches. Fixed canonical
  `liapoldus.github.io/public/spec/gateway.schema.json` and the mirrored copy
  `assets/contracts/gateway.schema.json`: `pathMatcher` is now
  `oneOf [string ^/, array<minItems 1 ^/>, object exact|prefix|regex ^/]`.
  TODO.md + docs site were updated after a User decision (fix canonical + copy).
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

## 0. Foundation

- [ ] Add the Go module, `cmd/gateway`, build targets, short README, Dockerfile
  and GitHub Actions for Ubuntu and macOS.
- [ ] Add `tests/package.json`, Vitest + tsx setup, process lifecycle helpers,
  temporary registry/config fixtures and contract downloader.
- [ ] Add red tests for CLI config discovery, missing config, invalid config,
  redacted config printing and validation without runtime mutation.
- [ ] Implement the smallest CLI/config compiler surface that makes those
  tests pass.

## 1. Configuration and snapshots

- [X] YAML decoding; includes/globs/cycle detection; variables and `env:` /
  `file:` secret references with redaction.
- [ ] JSON Schema plus semantic validation: named-resource references, regex,
  route terminal-action rules and management listener security rules.
- [X] Immutable compiled graph, SHA-256 digest/revision, prepare/swap/drain and
  rollback-on-failure semantics.
- [ ] CLI `serve`, `config validate|path|print|format|explain|diff`, typed
  diagnostics, exit codes and RFC 9457 problems.

## 2. HTTP and site registry

- [ ] HTTP listeners, ordered routes/matchers, rewrites, headers, redirects,
  policy pipeline and access telemetry.
- [ ] Directory and release static sources: `site.yaml`, traversal/symlink
  protection, SPA conditions, locales, ETag, conditional and range requests.
- [ ] Reverse proxy: health checks, DNS/static targets, balancing, retries,
  forwarded headers and WebSocket upgrade.
- [ ] Filesystem registry: staged immutable publish, locking/recovery,
  atomic current/previous changes, retention, rollback and idempotency.

## 3. Management and observability

- [ ] Local Bearer static token/service accounts, bcrypt key lifecycle and
  remote TLS + mTLS enforcement.
- [ ] All documented Management API resources, cursor pagination, operations,
  audit records, config `If-Match` and secret-safe responses.
- [ ] JSON logs, Prometheus, OTLP tracing/metrics, exporter-failure isolation
  and retention.

## 4. Security, TLS and L4

- [ ] TLS profiles/storage/reload, certificate selection, mTLS and issuer
  operation lifecycle.
- [ ] JWT/OIDC, WAF, rate limiting, source-IP/geo and captcha policy paths.
- [ ] TCP and UDP listener/rule engines, TLS passthrough/termination, upstream
  relays, flow limits and graceful shutdown.

## 5. Plugin integration

- [ ] Publish and pin `github.com/Liapoldus/pluginprotocol` v1.0.0 from the
  sibling `pluginprotocol` repository.
- [ ] Gateway plugin supervisor: loopback launch, manifest/health/config.apply
  handshake, restart/backoff, logs, RSS/call limits and scoped grants.
- [ ] HTTP capability, TCP stream and UDP flow dispatch using test plugin
  processes in `tests/fixtures/` only.

## 6. v1 acceptance and ownership transfer

- [ ] Pass all 50 current documentation golden vectors plus race, malformed
  input, shutdown/recovery and no-secret regression suites on macOS/Linux.
- [ ] Build and smoke-test the Docker image; ensure GitHub Actions reports all
  test and contract failures.
- [ ] Generate Gateway contracts in `core`, release them as versioned GitHub
  Release assets, then update `liapoldus.github.io` to consume them.
