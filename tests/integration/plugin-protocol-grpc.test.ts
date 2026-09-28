import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");

describe("Gateway plugin protocol v1 gRPC boundary", () => {
  it("uses the sibling v1 gRPC transport without the legacy custom framing API", () => {
    const protocol = readFileSync(resolve(root, "internal/infrastructure/plugins/protocol.go"), "utf8");
    const goMod = readFileSync(resolve(root, "go.mod"), "utf8");

    expect(goMod).toContain("github.com/Liapoldus/pluginprotocol v1.1.0");
    expect(goMod).toContain("replace github.com/Liapoldus/pluginprotocol => ../pluginprotocol");
    expect(protocol).toContain("github.com/Liapoldus/pluginprotocol/presentation/sdk");
    expect(protocol).toContain("pluginsdk.DialContext");
    expect(protocol).not.toContain("pluginprotocol/transport");
    expect(protocol).not.toContain("pluginprotocol/framing");
    expect(protocol).not.toContain("framing.Encode");
    expect(protocol).toContain("ConfigApply");
    expect(protocol).toContain("CheckHealth");
    expect(protocol).not.toContain("Stream(");
  });
});
