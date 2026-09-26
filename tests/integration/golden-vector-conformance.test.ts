import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

interface Vector {
  id: string;
  input: { keys?: string[] };
  expected: { valid?: boolean; code?: string };
}

interface PublishVector {
  id: string;
  input?: { current?: string; activation?: string };
  expected: {
    sameOperation?: boolean;
    activationCount?: number;
    status?: number;
    code?: string;
    activeRevisionChanged?: boolean;
    currentPointerUnchanged?: boolean;
    current?: string;
    previous?: string;
    runtime?: string;
  };
}

describe("Gateway golden-vector conformance", () => {
  it("executes the bootstrap unknown-field vector through the production validator", async () => {
    const vectorDocument = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: Vector[] };
    const schema = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/gateway.schema.json"), "utf8"),
    ) as { properties: Record<string, unknown> };
    const errors = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/errors.json"), "utf8"),
    ) as { errors: Array<{ code: string; status: number }> };
    const vector = vectorDocument.vectors.find(
      (candidate) => candidate.input.keys !== undefined && candidate.expected.valid === false,
    );
    expect(vector, "bootstrap rejection vector").toBeDefined();
    const unknownKeys = vector!.input.keys!.filter((key) => !(key in schema.properties));
    expect(unknownKeys).toHaveLength(1);

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-golden-vector-"));
    try {
      const template = await readFile(
        join(coreRoot, "tests/fixtures/golden-vectors/bootstrap-base.yaml"),
        "utf8",
      );
      const documentPath = join(directory, "gateway.yaml");
      await writeFile(documentPath, `${template}${unknownKeys[0]}: {}\n`, "utf8");
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/golden-vector-bootstrap", documentPath],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as { valid: boolean; code: string; status: number };

      expect(observed.valid).toBe(vector!.expected.valid);
      expect(observed.code).toBe(vector!.expected.code);
      expect(observed.status).toBe(
        errors.errors.find((problem) => problem.code === vector!.expected.code)?.status,
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("executes group publish safety vectors through SQLite and the Management API", async () => {
    const vectorDocument = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: PublishVector[] };
    const vectors = new Map(vectorDocument.vectors.map((vector) => [vector.id, vector]));
    const idempotent = vectors.get("group-publish-idempotent");
    const conflict = vectors.get("group-publish-key-conflict");
    const stale = vectors.get("group-stale-revision");
    const activationFailure = vectors.get("activation-failure-preserves-pointers");
    expect(idempotent).toBeDefined();
    expect(conflict).toBeDefined();
    expect(stale).toBeDefined();
    expect(activationFailure).toBeDefined();

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-golden-group-publish-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as {
        publish: { operationId?: string };
        publishRetry: { operationId?: string };
        activationCountAfterPublish: number;
        idempotencyConflictStatus: number;
        idempotencyConflictCode: string;
        currentAfterPublish: string | null;
        currentAfterConflict: string | null;
        staleRevisionStatus: number;
        staleRevisionCode: string;
        currentBeforeStale: string | null;
        pointersAfterStale: string | null;
        activationFailureStatus: number;
        activationFailureOperationState: string;
        currentBeforeActivationFailure: string | null;
        currentAfterActivationFailure: string | null;
        previousBeforeActivationFailure: string | null;
        previousAfterActivationFailure: string | null;
        runtimeAfterActivationFailure: string;
      };

      expect(observed.publishRetry.operationId === observed.publish.operationId).toBe(idempotent!.expected.sameOperation);
      expect(observed.activationCountAfterPublish).toBe(idempotent!.expected.activationCount);
      expect(observed.idempotencyConflictStatus).toBe(conflict!.expected.status);
      expect(observed.idempotencyConflictCode).toBe(conflict!.expected.code);
      expect(observed.currentAfterConflict !== observed.currentAfterPublish).toBe(conflict!.expected.activeRevisionChanged);
      expect(observed.staleRevisionStatus).toBe(stale!.expected.status);
      expect(observed.staleRevisionCode).toBe(stale!.expected.code);
      expect(observed.pointersAfterStale === observed.currentBeforeStale).toBe(stale!.expected.currentPointerUnchanged);
      expect(observed.activationFailureStatus).toBe(202);
      expect(observed.activationFailureOperationState).toBe("failed");
      expect(activationFailure!.input?.activation).toBe("failed");
      expect(activationFailure!.input?.current).toBe(activationFailure!.expected.current);
      expect(observed.currentAfterActivationFailure).toBe(observed.currentBeforeActivationFailure);
      expect(observed.previousAfterActivationFailure).toBe(observed.previousBeforeActivationFailure);
      expect(observed.runtimeAfterActivationFailure).toBe(activationFailure!.expected.runtime);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
