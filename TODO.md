# Gateway v1 delivery checklist

This is the execution order. Every checkbox is test-first: commit a failing
TypeScript test under `tests/` before the implementation that satisfies it.

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

- [ ] YAML decoding; includes/globs/cycle detection; variables and `env:` /
  `file:` secret references with redaction.
- [ ] JSON Schema plus semantic validation: named-resource references, regex,
  route terminal-action rules and management listener security rules.
- [ ] Immutable compiled graph, SHA-256 digest/revision, prepare/swap/drain and
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
