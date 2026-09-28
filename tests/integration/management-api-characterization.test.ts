import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const requestID = "<request-id>";

interface Observation {
  readonly status: number;
  readonly contentType: string;
  readonly requestID: boolean;
  readonly body: unknown;
}

function problem(status: number, code: string, detail: string): Observation {
  return {
    status,
    contentType: "application/problem+json",
    requestID: true,
    body: {
      type: "about:blank",
      title: code,
      status,
      code,
      detail,
      instance: "",
      requestId: requestID,
    },
  };
}

describe("Management API response characterization matrix", () => {
  it("preserves dispatch, authentication, response headers, and problem bodies", async () => {
    const { stdout } = await execFileAsync(
      "go",
      ["run", "./tests/fixtures/management-api-characterization"],
      { cwd: coreRoot },
    );
    const actual = JSON.parse(stdout) as Record<string, Observation>;

    expect(actual).toEqual({
      healthGet: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: { status: "ok", requestId: requestID },
      },
      healthHead: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: null,
      },
      healthPost: problem(405, "method_not_allowed", "health endpoint accepts GET and HEAD"),
      statusUnauthorized: problem(401, "management_bearer_required", "Management API требует действующий Bearer service key."),
      statusAuthenticated: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: {
          drift: false,
          dataPlaneReadiness: { state: "ready" },
          requestId: requestID,
        },
      },
      statusMTLSRequired: problem(401, "management_mtls_required", "Удалённый Management API требует валидную mTLS identity."),
      healthBeforeMTLS: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: { status: "ok", requestId: requestID },
      },
      serviceKeyListUnavailable: problem(503, "management_unavailable", "Не удалось выполнить операцию с постоянным состоянием Management API."),
      serviceKeyCreateUnavailable: problem(503, "management_unavailable", "Не удалось выполнить операцию с постоянным состоянием Management API."),
      pluginList: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: {
          items: [
            { id: "fixture-a", name: "Alpha", state: "ready" },
          ],
          nextCursor: "MQ",
          requestId: requestID,
        },
      },
      invalidPluginPagination: problem(400, "invalid_pagination", "limit must be between 1 and 100"),
      adminSurfaces: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: {
          items: [{ plugin: "fixture-a", namespace: "forms", version: "v1", title: "Forms", capabilities: ["admin.surface.get"] }],
          requestId: requestID,
        },
      },
      pluginDetail: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: { id: "fixture-a", name: "Alpha", state: "ready" },
      },
      pluginNotFound: problem(404, "plugin_not_found", "Указанный plugin instance не существует."),
      pluginAdminUnavailable: problem(503, "plugin_unavailable", "plugin admin surface is unavailable"),
      pluginAdminPostUnavailable: problem(503, "plugin_unavailable", "plugin admin surface is unavailable"),
      pluginRestartUnavailable: problem(501, "not_implemented", "plugin restart is unavailable"),
      operationUnavailable: problem(503, "management_unavailable", "Не удалось выполнить операцию с постоянным состоянием Management API."),
      auditEmpty: {
        status: 200,
        contentType: "application/json",
        requestID: true,
        body: { items: [], nextCursor: null, requestId: requestID },
      },
      unknownRoute: problem(404, "not_found", "resource not found"),
      retiredConfigRoute: problem(404, "not_found", "resource not found"),
      unsupportedPluginMethod: problem(404, "not_found", "resource not found"),
    });
  }, 30_000);
});
