import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

const pluginProtocolImport = /github\.com\/Liapoldus\/pluginprotocol/;

async function goFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(entries.map(async (entry) => {
    const path = `${directory}/${entry.name}`;
    return entry.isDirectory() ? goFiles(path) : entry.name.endsWith(".go") ? [path] : [];
  }));
  return files.flat().sort();
}

async function goSources(directories: string[]): Promise<Array<{ file: string; source: string }>> {
  const files = (await Promise.all(directories.map((directory) => goFiles(`${root}/${directory}`)))).flat();
  return Promise.all(files.map(async (file) => ({ file, source: await readFile(file, "utf8") })));
}

function relative(files: string[]): string[] {
  return files.map((file) => file.replace(`${root}/`, ""));
}

describe("Core peer protocol boundary", () => {
  it("never imports pluginprotocol from production code", async () => {
    const sources = await goSources(["internal", "cmd"]);
    const moduleRoot = await readFile(`${root}/contractassets.go`, "utf8");
    const offenders = sources.filter(({ source }) => pluginProtocolImport.test(source));
    expect(relative(offenders.map(({ file }) => file))).toEqual([]);
    expect(moduleRoot).not.toMatch(pluginProtocolImport);
  });

  it("never imports pluginprotocol from test fixtures", async () => {
    const sources = await goSources(["tests/fixtures"]);
    const offenders = sources.filter(({ source }) => pluginProtocolImport.test(source));
    expect(relative(offenders.map(({ file }) => file))).toEqual([]);
  });

  it("declares no pluginprotocol requirement in go.mod or go.sum", async () => {
    const [goMod, goSum] = await Promise.all([
      readFile(`${root}/go.mod`, "utf8"),
      readFile(`${root}/go.sum`, "utf8"),
    ]);
    expect(goMod).not.toMatch(pluginProtocolImport);
    expect(goSum).not.toMatch(pluginProtocolImport);
  });

  it("keeps no plugin process supervision or local launch adapter", async () => {
    const [sdkControl, files, sources] = await Promise.all([
      readFile(`${root}/internal/infrastructure/plugins/sdk_control.go`, "utf8"),
      readdir(`${root}/internal/infrastructure/plugins`),
      goSources(["internal", "cmd", "tests/fixtures"]),
    ]);

    // Lifecycle and directory adapters are listed explicitly so new files in
    // this boundary remain deliberate and reviewed. TLS revocation stays here
    // so presentation never imports the SDK transport implementation.
    expect(files.sort()).toEqual([
      "admin_dispatch.go",
      "admin_surface.go",
      "declared_replicas.go",
      "errors.go",
      "peer_directory.go",
      "peer_directory_broadcaster.go",
      "registered_reconciliation.go",
      "replica_directory.go",
      "replica_observation.go",
      "sdk_admin_control.go",
      "sdk_contract.go",
      "sdk_control.go",
      "sdk_control_inprocess_test.go",
      "sdk_control_validation_test.go",
      "tls_revocation.go",
    ]);
    expect(sdkControl).not.toMatch(/exec\.Command|"os\/exec"|net\.Listen|ListenLoopback|StartGrantBroker|StartRuntime/);

    // Core owns plugin process lifecycle in no form, in any layer: no child
    // process launch, no supervision/backoff knobs, no peer transport.
    const supervision = /exec\.Command|"os\/exec"|exec\.Cmd\b|StartRuntime|StartSupervisor|StartGrantBroker|restartEnabled|restartInitialBackoff|restartMaximumBackoff|healthFailureThreshold|memoryLimitBytes|plugin_release|plugin_catalog|pluginv1|protojson/;
    expect(relative(sources.filter(({ file, source }) => !file.includes("tests/fixtures/") && supervision.test(source)).map(({ file }) => file))).toEqual([]);
  });

  it("carries no binary release or catalog install path in the plugin lifecycle layer", async () => {
    const sources = await goSources(["internal/infrastructure/plugins", "internal/runtime", "cmd", "tests/fixtures"]);
    const releaseInstall = /plugin_catalog|pluginCatalog|plugin_release|pluginRelease|PluginCatalog|PluginRelease|tuf|TUF/;
    expect(relative(sources.filter(({ source }) => releaseInstall.test(source)).map(({ file }) => file))).toEqual([]);
  });
});
