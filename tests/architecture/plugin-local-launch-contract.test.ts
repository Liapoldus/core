import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const coreRoot = join(import.meta.dirname, "../..");

describe("local plugin launch and configuration boundary", () => {
  it("allows only an executable path and fixes the config-secret bound in assets", async () => {
    const launchSchema = JSON.parse(await readFile(join(coreRoot, "assets/contracts/local-launch.schema.json"), "utf8")) as {
      required: string[];
      properties: Record<string, unknown>;
      additionalProperties: boolean;
    };
    const runtime = JSON.parse(await readFile(join(coreRoot, "assets/contracts/plugin-runtime.json"), "utf8")) as {
      configSecrets: { maximumBytes: number; requireAbsolutePath: boolean };
    };

    expect(launchSchema.required).toEqual(["binary"]);
    expect(launchSchema.properties).toEqual({ binary: expect.any(Object) });
    expect(launchSchema.additionalProperties).toBe(false);
    expect(runtime.configSecrets).toEqual({
      maximumBytes: 65_536,
      requireAbsolutePath: true,
      grantPurpose: expect.any(String),
    });
  });

  it("starts children without argv or inherited environment and passes only the listener FD", async () => {
    const supervisor = await readFile(join(coreRoot, "internal/infrastructure/plugins/supervisor.go"), "utf8");
    const launch = await readFile(join(coreRoot, "internal/infrastructure/plugins/local_launch.go"), "utf8");
    const protocol = await readFile(join(coreRoot, "internal/infrastructure/plugins/protocol.go"), "utf8");

    expect(supervisor).toContain("exec.CommandContext(ctx, spec.Binary)");
    expect(supervisor).toContain("cmd.Env = []string{}");
    expect(supervisor).toContain("cmd.ExtraFiles = []*os.File{listenerFile}");
    expect(launch).toContain("preparePluginSettings");
    expect(protocol).toContain("BootstrapAndHandshake");
    expect(launch).not.toContain("github.com/Liapoldus/core\"");
    expect(launch).not.toContain("internal/infrastructure/config");
  });

  it("pushes settings before readiness and never puts settings into Caddy dispatch", async () => {
    const runtime = await readFile(join(coreRoot, "internal/infrastructure/plugins/runtime.go"), "utf8");
    const caddyHandler = await readFile(join(coreRoot, "internal/infrastructure/caddy/plugin_handler.go"), "utf8");
    const dispatch = await readFile(join(coreRoot, "internal/infrastructure/plugins/grant_broker.go"), "utf8");

    expect(runtime).toContain("issueConfig()");
    expect(runtime).toContain("BootstrapAndHandshake");
    expect(dispatch).toContain("GRANT_SCOPE_CONFIG_APPLY");
    expect(dispatch).toContain("request.GetSettingsRevision() != grant.settingsRevision");
    expect(caddyHandler).toContain("client.VerifyReady(ctx)");
    expect(caddyHandler).not.toContain("ConfigApply");
    expect(caddyHandler).not.toContain("Settings");
  });
});
