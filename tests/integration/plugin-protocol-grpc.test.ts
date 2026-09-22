import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");

describe("Gateway plugin protocol v1 gRPC boundary", () => {
  it("uses the sibling v1 gRPC transport without the legacy custom framing API", () => {
    const protocol = readFileSync(resolve(root, "internal/infrastructure/plugins/protocol.go"), "utf8");
    const capability = readFileSync(resolve(root, "internal/infrastructure/plugins/capability_dispatch.go"), "utf8");
    const goMod = readFileSync(resolve(root, "go.mod"), "utf8");

    expect(goMod).toContain("github.com/Liapoldus/pluginprotocol v1.1.0");
    expect(goMod).toContain("replace github.com/Liapoldus/pluginprotocol => ../pluginprotocol");
    expect(protocol).toContain("github.com/Liapoldus/pluginprotocol/transport");
    expect(protocol).toContain("transport.DialContext");
    expect(protocol).not.toContain("pluginprotocol/framing");
    expect(protocol).not.toContain("framing.Encode");
    expect(capability).toContain("HTTPRequest");
    expect(capability).toContain("L4Request");
    expect(capability).not.toContain("CallRawJSON");
  });

  it("keeps HTTP and L4 boundary DTOs as JSON rather than protobuf business messages", () => {
    const capability = readFileSync(resolve(root, "internal/infrastructure/plugins/capability_dispatch.go"), "utf8");
    expect(capability).toContain('json:"method"');
    expect(capability).toContain('json:"path"');
    expect(capability).toContain('json:"transport"');
    expect(capability).toContain('json:"connectionId"');
    expect(capability).toContain("json.Marshal(request)");
  });
});
