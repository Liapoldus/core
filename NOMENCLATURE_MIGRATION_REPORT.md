# Core: breaking nomenclature migration — 2026-10-13

Scope: `/Users/docup/Projects/Liapoldus Engine/core` only. Initial Core working tree was clean. No sibling sources, workspace instructions/status, commits or pushes were changed.

## Implemented

- Core module remains `github.com/Liapoldus/core/v3`; SDK remains major v2 at `github.com/Liapoldus/service-sdk/v2`, resolved with `replace => ../service-sdk`. Production Core and its fixtures declare/import no Wool lifecycle dependency.
- Runtime packages, models, ports, JSON, routes, errors, operation kinds and newly emitted audit events use Service nomenclature. Management roots are `/api/services` and `/api/service-links`; retired roots return 404 without redirects. Only editor extensions are StudioPlugins. Control plane: Studio → CLI → Core → Service SDK REST+mTLS. Independent Services use Wool for data plane.
- Removed Core in-process lifecycle dispatch, registrations, direct snapshot publication and REST-disable options. Host API still hosts Core; every Service uses REST registration and leases.
- SQLite v15 is a separate transaction. It renames eight runtime tables and two indexes; it preserves neutral IDs, manifest bytes, configuration bytes/digests/schema versions/slots, replica/lease/rollout/policy rows and completed operation/audit history. Known pending/running operation kinds migrate so recovery can continue.
- Operational settings owner appends new immutable serviceControl revisions and moves desired/effective pointers independently. Original settings revisions and audit records remain intact. Conflicting keys abort the entire v15 transaction. Repeat startup appends nothing.
- Frozen v3–v14 SchemaSQL and its original digest remain unchanged. Historical query SQL keeps its original fingerprint; historical v5 fixture seeds original names/data. Neutral golden-vector IDs and application method strings are preserved.
- Contract owner publication regenerates v1 mirrors and v1/v2 manifest digests; v2 traffic-field mirror is regenerated from its Core owner asset. Owner docs, instructions, CI/Docker dependencies, fixtures and source-boundary tests are migrated.
- Architecture permits exactly the storage migration coordinator → settings owner dependency in the infrastructure layer. Sibling fixture builds use `-mod=readonly`.

## Checks

Node PATH: `/Users/docup/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:$PATH`. Go independence checks use `GOWORK=off`.

| Command | Result |
| --- | --- |
| `GOWORK=off go test ./...` | PASS, including native migration/history/transaction/pointer tests |
| `GOWORK=off go build ./...` | PASS |
| `GOWORK=off go vet ./...` | PASS |
| `GOWORK=off make staticcheck-u1000` | PASS |
| `GOWORK=off make arch-lint` | PASS, pinned Docker image, no warnings |
| `GOWORK=off go test -race ./internal/infrastructure/storage ./internal/infrastructure/storage/settings ./internal/infrastructure/services ./internal/presentation/api ./internal/runtime` | PASS |
| `GOWORK=off make test-race` | PASS, real compiled-process race-detection fixture |
| `GOWORK=off make docker-smoke` | PASS |
| `npm --prefix tests run typecheck` | PASS |
| `npm --prefix tests run lint` | PASS |
| `node scripts/publish-contracts.mjs` | PASS, regenerated owner mirrors/digests |
| `node scripts/verify-contract-bundle.mjs contracts/v1` | PASS |
| `node scripts/verify-contract-bundle.mjs contracts/v2` | PASS |
| `git diff --check` | PASS |
| `GOWORK=off go list -m github.com/Liapoldus/service-sdk/v2` | v2.0.0 => ../service-sdk |
| Focused TS migration/API/rollout/contracts/ownership checks | 16 suites / 36 tests PASS; full run also passes new REST-only boundary test |
| `LIAPOLDUS_DOCS_USE_WORKTREE=1 PATH=<bundled-node> GOWORK=off make check` | PASS: Go build/tests, 95 Vitest files passed / 1 skipped (157 tests passed), TypeScript typecheck/lint, pinned Docker architecture lint. |
| `npm --prefix tests test -- integration/manual-core-server.test.ts` | PASS: real Core, Server and forms-db processes, registration/configuration, Server HTTP, direct Wool peer call, Core outage/restart and trust/revocation checks. |

Cross-builds passed:

```sh
GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/core-nomenclature-checks.IMqQum/core-linux-amd64 ./cmd/core
GOWORK=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/core-nomenclature-checks.IMqQum/core-linux-arm64 ./cmd/core
GOWORK=off GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/core-nomenclature-checks.IMqQum/core-darwin-amd64 ./cmd/core
GOWORK=off GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/core-nomenclature-checks.IMqQum/core-darwin-arm64 ./cmd/core
```

Focused TS command:

```sh
GOWORK=off npm --prefix tests test -- --run management-paths structure traffic-rollout-storage service-config-migration sqlite-metadata registered-rollout service-settings-recovery wool-sdk-boundary contract-publication contracts-v1 traffic-rollout-contract traffic-controller-contract dead-artifacts approved-orphan-symbols
```

Logs: `/tmp/core-nomenclature-checks.IMqQum/{make-check.log,go-test.log,race.log,docker-smoke.log}`.

## Current handoff

All previously listed local consumer mismatches are resolved. The Core/Server/forms-db
process smoke and full Core check pass with the local coordinated worktree sources;
the shared `services/go.work`, standalone active-service Go tests, Studio `make check`,
and VitePress docs test/build also pass. Core continues to reject retired API paths
and ships no compatibility aliases.

Publication is still blocked externally: `Liapoldus/service-sdk` and `Liapoldus/wool`
do not yet exist at their target GitHub URLs, and GitHub CLI is unauthenticated.
An organization owner must rename/publish those repositories before exact remote
docs SHA pins, hosted CI, or release claims can be verified. No commits or pushes
were made during this local migration.

## Exact changed paths

All paths below are relative to the Core root above. Renames include their content changes. This inventory includes this report.

### Modified existing paths (161)

```text
.github/workflows/integration.yml
.github/workflows/verify.yml
.gitignore
.go-arch-lint.yml
AGENTS.md
Dockerfile
TODO.md
assets/contracts/audit-fields.yaml
assets/contracts/errors.json
assets/contracts/management-fields.yaml
assets/contracts/sqlite-runtime.yaml
assets/contracts/v2/traffic-rollout-fields.yaml
contractassets.go
contracts/v1/errors.json
contracts/v1/management-fields.yaml
contracts/v1/management.openapi.yaml
contracts/v1/manifest.json
contracts/v1/security-runtime.json
contracts/v2/manifest.json
contracts/v2/traffic-controller.schema.json
contracts/v2/traffic-rollout-fields.yaml
contracts/v2/traffic-rollout.openapi.yaml
docs/site/core/api/audit.md
docs/site/core/api/authentication.md
docs/site/core/api/config.md
docs/site/core/api/index.md
docs/site/core/api/openapi.md
docs/site/core/api/operations.md
docs/site/core/api/sites.md
docs/site/core/architecture/control-plane.md
docs/site/core/architecture/cookies.md
docs/site/core/architecture/core.md
docs/site/core/architecture/guide.md
docs/site/core/architecture/implementation.md
docs/site/core/architecture/index.md
docs/site/core/architecture/protocol.md
docs/site/core/architecture/roadmap.md
docs/site/core/architecture/structure.md
docs/site/core/architecture/target.md
docs/site/core/architecture/v3.md
docs/site/core/configuration/acceptance.md
docs/site/core/configuration/bootstrap.md
docs/site/core/configuration/errors.md
docs/site/core/configuration/http-runtime.md
docs/site/core/configuration/index.md
docs/site/core/configuration/secrets.md
docs/site/core/configuration/security.md
docs/site/core/configuration/tls-reload.md
docs/site/core/configuration/transports.md
docs/site/core/configuration/upstreams.md
docs/site/core/deploy/backup-restore.md
docs/site/core/deploy/index.md
docs/site/core/deploy/observability.md
docs/site/core/examples/captcha.md
docs/site/core/examples/index.md
docs/site/core/examples/tcp.md
docs/site/core/index.md
docs/site/diagrams/core-config-store.mmd
go.mod
go.sum
host/host.go
internal/application/config_bundle_service.go
internal/application/traffic_rollout_cohorts.go
internal/application/traffic_rollout_service.go
internal/domain/interfaces/traffic_rollout_store.go
internal/domain/models/peer_contract_range.go
internal/domain/models/peer_link_rule.go
internal/domain/models/traffic_rollout_plan.go
internal/infrastructure/config/bootstrap.go
internal/infrastructure/config/contracts.go
internal/infrastructure/config/definitions.go
internal/infrastructure/config/errors.go
internal/infrastructure/config/peer_endpoint.go
internal/infrastructure/config/settings.go
internal/infrastructure/config/settings_test.go
internal/infrastructure/config/traffic_rollout.go
internal/infrastructure/config/validator.go
internal/infrastructure/storage/definitions.go
internal/infrastructure/storage/definitions_test.go
internal/infrastructure/storage/queries_test.go
internal/infrastructure/storage/settings/store.go
internal/infrastructure/storage/sqlite.go
internal/infrastructure/storage/sqlite_traffic_rollout_store.go
internal/infrastructure/storage/traffic_rollout_queries.go
internal/presentation/api/handlers/admin_surface.go
internal/presentation/api/handlers/deps.go
internal/presentation/api/handlers/traffic_rollouts.go
internal/presentation/api/router.go
internal/presentation/api/server.go
internal/presentation/api/traffic_controller.go
internal/runtime/bootstrap.go
internal/runtime/inventory.go
internal/runtime/replica_readiness_monitor.go
internal/runtime/settings.go
internal/runtime/traffic_controller.go
scripts/docker-smoke.sh
scripts/publish-contracts.mjs
tests/architecture/api-codec-file-ownership.test.ts
tests/architecture/api-router-file-ownership.test.ts
tests/architecture/arch-lint-remediation.test.ts
tests/architecture/cli-bootstrap-ownership.test.ts
tests/architecture/dead-artifacts.test.ts
tests/architecture/docker-workspace.test.ts
tests/architecture/management-openapi-surface.test.ts
tests/architecture/management-paths.test.ts
tests/architecture/presentation-boundaries.test.ts
tests/architecture/structure.test.ts
tests/architecture/traffic-rollout-contract.test.ts
tests/fixtures/link-policy-snapshot/main.go
tests/fixtures/management-api-characterization/main.go
tests/fixtures/manual-core-server/main.go
tests/fixtures/operation-persistence/main.go
tests/fixtures/peer-directory-assembly/main.go
tests/fixtures/peer-directory-conformance/main.go
tests/fixtures/registered-release-cohort/main.go
tests/fixtures/registered-replica-reconciliation/main.go
tests/fixtures/registered-replica-reload/main.go
tests/fixtures/registered-rollout-cohort/main.go
tests/fixtures/registered-rollout-exact-dispatch/main.go
tests/fixtures/registered-rollout-operation/main.go
tests/fixtures/registered-rollout-target-lost/main.go
tests/fixtures/registered-source-recovery/main.go
tests/fixtures/release-cohort-activation/main.go
tests/fixtures/serve-domain/main.go
tests/fixtures/serve-self-registration/main.go
tests/fixtures/traffic-rollout-api/main.go
tests/fixtures/traffic-rollout-plan/main.go
tests/fixtures/traffic-rollout-reload/main.go
tests/fixtures/traffic-rollout-service/main.go
tests/fixtures/traffic-rollout-store/main.go
tests/fixtures/traffic-rollout-transaction/main.go
tests/integration/link-policy-snapshot.test.ts
tests/integration/management-api-characterization.test.ts
tests/integration/management-api.test.ts
tests/integration/management-route-dispatch.test.ts
tests/integration/manual-core-server-sql.test.ts
tests/integration/manual-core-server.test.ts
tests/integration/operation-persistence.test.ts
tests/integration/peer-directory-assembly.test.ts
tests/integration/peer-directory-conformance.test.ts
tests/integration/peer-directory-poll.test.ts
tests/integration/race-detection.test.ts
tests/integration/registered-instance-source-recovery.test.ts
tests/integration/registered-release-cohort-activation.test.ts
tests/integration/registered-release-cohort.test.ts
tests/integration/registered-rollout-cohort.test.ts
tests/integration/serve-domain.test.ts
tests/integration/serve-self-registration.test.ts
tests/integration/service-key-issuance.test.ts
tests/integration/service-key-list.test.ts
tests/integration/sqlite-integrity.test.ts
tests/integration/sqlite-metadata.test.ts
tests/integration/traffic-controller-listener.test.ts
tests/integration/traffic-rollout-service.test.ts
tests/integration/traffic-rollout-storage.test.ts
tests/integration/traffic-rollout-transaction.test.ts
tests/package-lock.json
tests/support/global-go-workspace.ts
tests/support/initialize.ts
tests/support/workspace.ts
tests/unit/approved-orphan-symbols.test.ts
```

### Renamed paths (127)

```text
assets/contracts/plugin-reconciliation.yaml -> assets/contracts/service-reconciliation.yaml
assets/contracts/plugin-secret-grants.yaml -> assets/contracts/service-secret-grants.yaml
assets/contracts/sqlite-plugin-instances.yaml -> assets/contracts/sqlite-service-instances.yaml
docs/site/core/architecture/plugin-deployment.md -> docs/site/core/architecture/service-deployment.md
internal/application/plugin_configuration_service.go -> internal/application/service_configuration_service.go
internal/application/plugin_link_policy_service.go -> internal/application/service_link_policy_service.go
internal/application/plugin_secret_grant_service.go -> internal/application/service_secret_grant_service.go
internal/domain/interfaces/plugin_configuration_applier.go -> internal/domain/interfaces/service_configuration_applier.go
internal/domain/interfaces/plugin_configuration_instances.go -> internal/domain/interfaces/service_configuration_instances.go
internal/domain/interfaces/plugin_configuration_reader.go -> internal/domain/interfaces/service_configuration_reader.go
internal/domain/interfaces/plugin_configuration_rollout_applier.go -> internal/domain/interfaces/service_configuration_rollout_applier.go
internal/domain/interfaces/plugin_configuration_rollout.go -> internal/domain/interfaces/service_configuration_rollout.go
internal/domain/interfaces/plugin_configuration_store.go -> internal/domain/interfaces/service_configuration_store.go
internal/domain/interfaces/plugin_link_policy_reader.go -> internal/domain/interfaces/service_link_policy_reader.go
internal/domain/interfaces/plugin_link_policy_store.go -> internal/domain/interfaces/service_link_policy_store.go
internal/domain/interfaces/plugin_replica_identity_resolver.go -> internal/domain/interfaces/service_replica_identity_resolver.go
internal/domain/interfaces/plugin_retained_configuration_reader.go -> internal/domain/interfaces/service_retained_configuration_reader.go
internal/domain/interfaces/plugin_secret_reference_resolver.go -> internal/domain/interfaces/service_secret_reference_resolver.go
internal/domain/models/plugin_configuration_conflict.go -> internal/domain/models/service_configuration_conflict.go
internal/domain/models/plugin_configuration_convergence_pending.go -> internal/domain/models/service_configuration_convergence_pending.go
internal/domain/models/plugin_configuration_not_found.go -> internal/domain/models/service_configuration_not_found.go
internal/domain/models/plugin_configuration_payload_too_large.go -> internal/domain/models/service_configuration_payload_too_large.go
internal/domain/models/plugin_configuration_pointers.go -> internal/domain/models/service_configuration_pointers.go
internal/domain/models/plugin_configuration_rejected.go -> internal/domain/models/service_configuration_rejected.go
internal/domain/models/plugin_configuration_revision.go -> internal/domain/models/service_configuration_revision.go
internal/domain/models/plugin_configuration_rollout.go -> internal/domain/models/service_configuration_rollout.go
internal/domain/models/plugin_configuration_unavailable.go -> internal/domain/models/service_configuration_unavailable.go
internal/domain/models/plugin_instance.go -> internal/domain/models/service_instance.go
internal/domain/models/plugin_link_policy.go -> internal/domain/models/service_link_policy.go
internal/domain/models/plugin_metadata.go -> internal/domain/models/service_metadata.go
internal/domain/models/plugin_registration_result.go -> internal/domain/models/service_registration_result.go
internal/domain/models/plugin_registration.go -> internal/domain/models/service_registration.go
internal/domain/models/plugin_replica_identity.go -> internal/domain/models/service_replica_identity.go
internal/domain/models/plugin_rollout_target.go -> internal/domain/models/service_rollout_target.go
internal/domain/models/plugin_secret_grant_failure_kind.go -> internal/domain/models/service_secret_grant_failure_kind.go
internal/domain/models/plugin_secret_grant_failure.go -> internal/domain/models/service_secret_grant_failure.go
internal/domain/models/plugin_secret_grant_receipt.go -> internal/domain/models/service_secret_grant_receipt.go
internal/domain/models/plugin_secret_grant.go -> internal/domain/models/service_secret_grant.go
internal/infrastructure/config/plugin_inventory.go -> internal/infrastructure/config/service_inventory.go
internal/infrastructure/config/plugin_reconciliation.go -> internal/infrastructure/config/service_reconciliation.go
internal/infrastructure/config/plugin_secret_grants.go -> internal/infrastructure/config/service_secret_grants.go
internal/infrastructure/plugins/admin_dispatch.go -> internal/infrastructure/services/admin_dispatch.go
internal/infrastructure/plugins/admin_surface.go -> internal/infrastructure/services/admin_surface.go
internal/infrastructure/plugins/declared_replicas.go -> internal/infrastructure/services/declared_replicas.go
internal/infrastructure/plugins/errors.go -> internal/infrastructure/services/errors.go
internal/infrastructure/plugins/peer_directory_broadcaster.go -> internal/infrastructure/services/peer_directory_broadcaster.go
internal/infrastructure/plugins/peer_directory.go -> internal/infrastructure/services/peer_directory.go
internal/infrastructure/plugins/registered_reconciliation.go -> internal/infrastructure/services/registered_reconciliation.go
internal/infrastructure/plugins/replica_directory.go -> internal/infrastructure/services/replica_directory.go
internal/infrastructure/plugins/replica_observation.go -> internal/infrastructure/services/replica_observation.go
internal/infrastructure/plugins/sdk_admin_control.go -> internal/infrastructure/services/sdk_admin_control.go
internal/infrastructure/plugins/sdk_contract.go -> internal/infrastructure/services/sdk_contract.go
internal/infrastructure/plugins/sdk_control_validation_test.go -> internal/infrastructure/services/sdk_control_validation_test.go
internal/infrastructure/plugins/sdk_control.go -> internal/infrastructure/services/sdk_control.go
internal/infrastructure/plugins/sdk_metadata_test.go -> internal/infrastructure/services/sdk_metadata_test.go
internal/infrastructure/plugins/tls_revocation.go -> internal/infrastructure/services/tls_revocation.go
internal/infrastructure/storage/plugin_configuration_queries.go -> internal/infrastructure/storage/historical_configuration_queries.go
internal/infrastructure/storage/plugin_configuration_snapshot.go -> internal/infrastructure/storage/service_configuration_snapshot.go
internal/infrastructure/storage/plugin_convergence_snapshot.go -> internal/infrastructure/storage/service_convergence_snapshot.go
internal/infrastructure/storage/plugin_link_policy_snapshot.go -> internal/infrastructure/storage/service_link_policy_snapshot.go
internal/infrastructure/storage/plugin_registration_queries.go -> internal/infrastructure/storage/service_registration_queries.go
internal/infrastructure/storage/sqlite_plugin_configuration_store.go -> internal/infrastructure/storage/sqlite_service_configuration_store.go
internal/infrastructure/storage/sqlite_plugin_instances.go -> internal/infrastructure/storage/sqlite_service_instances.go
internal/infrastructure/storage/sqlite_plugin_link_policy_store.go -> internal/infrastructure/storage/sqlite_service_link_policy_store.go
internal/infrastructure/storage/sqlite_plugin_replicas.go -> internal/infrastructure/storage/sqlite_service_replicas.go
internal/infrastructure/storage/sqlite_plugin_rollouts.go -> internal/infrastructure/storage/sqlite_service_rollouts.go
internal/presentation/api/handlers/plugin_links.go -> internal/presentation/api/handlers/service_links.go
internal/presentation/api/handlers/plugin_metadata_test.go -> internal/presentation/api/handlers/service_metadata_test.go
internal/presentation/api/handlers/plugin_metadata.go -> internal/presentation/api/handlers/service_metadata.go
internal/presentation/api/handlers/plugin_settings_rollback.go -> internal/presentation/api/handlers/service_settings_rollback.go
internal/presentation/api/handlers/plugin_settings_update.go -> internal/presentation/api/handlers/service_settings_update.go
internal/presentation/api/handlers/plugins.go -> internal/presentation/api/handlers/services.go
internal/presentation/api/plugin_config_pull.go -> internal/presentation/api/service_config_pull.go
internal/presentation/api/plugin_peer_directory.go -> internal/presentation/api/service_peer_directory.go
internal/presentation/api/plugin_replica_lifecycle.go -> internal/presentation/api/service_replica_lifecycle.go
internal/presentation/api/plugin_secret_grants.go -> internal/presentation/api/service_secret_grants.go
internal/runtime/plugin_control_plane.go -> internal/runtime/service_control_plane.go
internal/runtime/plugin_registration.go -> internal/runtime/service_registration.go
internal/runtime/plugin_rest_control.go -> internal/runtime/service_rest_control.go
tests/architecture/api-plugin-handler-ownership.test.ts -> tests/architecture/api-service-handler-ownership.test.ts
tests/architecture/plugin-admin-surface-boundary.test.ts -> tests/architecture/service-admin-surface-boundary.test.ts
tests/architecture/plugin-agnostic.test.ts -> tests/architecture/service-agnostic.test.ts
tests/architecture/plugin-ipc-ownership.test.ts -> tests/architecture/service-ipc-ownership.test.ts
tests/architecture/pluginprotocol-sdk-boundary.test.ts -> tests/architecture/wool-sdk-boundary.test.ts
tests/fixtures/plugin-admin-surface-forwarding/main.go -> tests/fixtures/service-admin-surface-forwarding/main.go
tests/fixtures/plugin-artifact-forwarding/main.go -> tests/fixtures/service-artifact-forwarding/main.go
tests/fixtures/plugin-config-apply-service/main.go -> tests/fixtures/service-config-apply-service/main.go
tests/fixtures/plugin-config-concurrency/main.go -> tests/fixtures/service-config-concurrency/main.go
tests/fixtures/plugin-config-migration-v5/main.go -> tests/fixtures/service-config-migration-v5/main.go
tests/fixtures/plugin-config-revisions/main.go -> tests/fixtures/service-config-revisions/main.go
tests/fixtures/plugin-config-roll-forward/main.go -> tests/fixtures/service-config-roll-forward/main.go
tests/fixtures/plugin-config-snapshot/main.go -> tests/fixtures/service-config-snapshot/main.go
tests/fixtures/plugin-convergence-snapshot/main.go -> tests/fixtures/service-convergence-snapshot/main.go
tests/fixtures/plugin-link-policy-store/main.go -> tests/fixtures/service-link-policy-store/main.go
tests/fixtures/plugin-links-api/main.go -> tests/fixtures/service-links-api/main.go
tests/fixtures/plugin-peer-directory/main.go -> tests/fixtures/service-peer-directory/main.go
tests/fixtures/plugin-peer-identity/main.go -> tests/fixtures/service-peer-identity/main.go
tests/fixtures/plugin-reload-fanout/main.go -> tests/fixtures/service-reload-fanout/main.go
tests/fixtures/plugin-replica-directory/main.go -> tests/fixtures/service-replica-directory/main.go
tests/fixtures/plugin-sdk-config-pull/main.go -> tests/fixtures/service-sdk-config-pull/main.go
tests/fixtures/plugin-secret-grants/main.go -> tests/fixtures/service-secret-grants/main.go
tests/fixtures/plugin-settings-api/main.go -> tests/fixtures/service-settings-api/main.go
tests/fixtures/plugin-settings-mutation/main.go -> tests/fixtures/service-settings-mutation/main.go
tests/fixtures/plugin-settings-recovery/main.go -> tests/fixtures/service-settings-recovery/main.go
tests/fixtures/plugin-settings-rollback/main.go -> tests/fixtures/service-settings-rollback/main.go
tests/fixtures/serve-plugin-runtime/main.go -> tests/fixtures/serve-service-runtime/main.go
tests/integration/plugin-admin-surface-forwarding.test.ts -> tests/integration/service-admin-surface-forwarding.test.ts
tests/integration/plugin-admin-surface.test.ts -> tests/integration/service-admin-surface.test.ts
tests/integration/plugin-artifact-forwarding.test.ts -> tests/integration/service-artifact-forwarding.test.ts
tests/integration/plugin-config-apply-service.test.ts -> tests/integration/service-config-apply-service.test.ts
tests/integration/plugin-config-migration.test.ts -> tests/integration/service-config-migration.test.ts
tests/integration/plugin-config-roll-forward.test.ts -> tests/integration/service-config-roll-forward.test.ts
tests/integration/plugin-config-snapshot.test.ts -> tests/integration/service-config-snapshot.test.ts
tests/integration/plugin-configuration-revisions.test.ts -> tests/integration/service-configuration-revisions.test.ts
tests/integration/plugin-convergence-snapshot.test.ts -> tests/integration/service-convergence-snapshot.test.ts
tests/integration/plugin-link-policy-store.test.ts -> tests/integration/service-link-policy-store.test.ts
tests/integration/plugin-links-api.test.ts -> tests/integration/service-links-api.test.ts
tests/integration/plugin-peer-identity.test.ts -> tests/integration/service-peer-identity.test.ts
tests/integration/plugin-reload-fanout.test.ts -> tests/integration/service-reload-fanout.test.ts
tests/integration/plugin-replica-directory.test.ts -> tests/integration/service-replica-directory.test.ts
tests/integration/plugin-sdk-config-pull.test.ts -> tests/integration/service-sdk-config-pull.test.ts
tests/integration/plugin-secret-grants.test.ts -> tests/integration/service-secret-grants.test.ts
tests/integration/plugin-settings-api.test.ts -> tests/integration/service-settings-api.test.ts
tests/integration/plugin-settings-mutation.test.ts -> tests/integration/service-settings-mutation.test.ts
tests/integration/plugin-settings-recovery.test.ts -> tests/integration/service-settings-recovery.test.ts
tests/integration/plugin-settings-rollback.test.ts -> tests/integration/service-settings-rollback.test.ts
tests/integration/serve-plugin-runtime.test.ts -> tests/integration/serve-service-runtime.test.ts
```

### Added paths (7)

```text
NOMENCLATURE_MIGRATION_REPORT.md
internal/infrastructure/services/sdk_control_rest_test.go
internal/infrastructure/storage/service_configuration_queries.go
internal/infrastructure/storage/service_nomenclature.go
internal/infrastructure/storage/service_nomenclature_test.go
internal/infrastructure/storage/settings/nomenclature.go
internal/presentation/api/service_routes_test.go
```

### Removed paths (1)

```text
internal/infrastructure/plugins/sdk_control_inprocess_test.go
```
