import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createGoWorkspace, missingWorkspaceRepositories } from "../support/workspace.js";

const root = resolve(import.meta.dirname, "../..");
const missing = missingWorkspaceRepositories(
  "pluginprotocol",
  "plugin-sdk",
  "plugins/server",
  "plugins/forms-db",
);

const databases = [
  { driver: "postgres", variable: "LIAPOLDUS_V1_E2E_POSTGRES_DSN" },
  { driver: "mysql", variable: "LIAPOLDUS_V1_E2E_MYSQL_DSN" },
  { driver: "mysql", variable: "LIAPOLDUS_V1_E2E_MARIADB_DSN" },
];
const configuredDatabases = databases.filter(({ variable }) => process.env[variable]);

describe.skipIf(missing.length > 0 || configuredDatabases.length === 0)(
  "production Core to forms-db SQL lifecycle",
  () => {
    it.each(configuredDatabases)("uses $driver storage durably through Core and Server", ({ driver, variable }) => {
      const dsn = process.env[variable];
      if (!dsn) throw new Error("configured SQL integration lost its DSN");
      const workspace = createGoWorkspace("core", "plugin-sdk", "pluginprotocol", "plugins/server", "plugins/forms-db");
      let output: string;
      try {
        output = execFileSync("go", ["run", "./tests/fixtures/manual-core-server"], {
          cwd: root,
          encoding: "utf8",
          timeout: 300_000,
          env: {
            ...process.env,
            GOWORK: workspace.path,
            GOTOOLCHAIN: "go1.26.0",
            LIAPOLDUS_V1_E2E_FORMS_DRIVER: driver,
            LIAPOLDUS_V1_E2E_FORMS_DSN: dsn,
          },
        });
      } finally {
        workspace.cleanup();
      }
      const result = JSON.parse(output);
      expect(result.formsDatabaseDriver).toBe(driver);
      expect(result.staleProductionGrantDenied).toBe(true);
      expect(result.formsListWithoutLeaseRejected).toBe(true);
      expect(result.formsListRevokedReplicaDenied).toBe(true);
      expect(result.coreDidNotObservePeerPayload).toBe(true);
      expect(result.incomingCookiesRedactedEverywhere).toBe(true);
      expect(result.coordinatedTrustRootRotationConverged).toBe(true);
      expect(result.formsListRoundTrip).toBe(true);
      expect(result.formsConcurrentRoundTrip).toBe(true);
      expect(result.formsCandidateRefusalPreserved).toBe(true);
      expect(result.formsCandidateRollbackRecovered).toBe(true);
      expect(result.formsListAfterCandidateRollback).toBe(true);
      expect(result.formsPeerReconnected).toBe(true);
      expect(result.childNoSQLDSNEnvironment).toBe(true);
      expect(result.formsDataPersistedAfterRestart).toBe(true);
      expect(result.formsStorageUnavailableDuringOutage).toBe(true);
      expect(result.formsStorageRecoveredAfterOutage).toBe(true);
    }, 300_000);
  },
);
