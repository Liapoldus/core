import { existsSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, cleanupGatewayTestBinary } from "../support/gateway.js";

describe("Gateway test binary lifecycle", () => {
  it("removes its temporary executable and directory on cleanup", async () => {
    const executable = await buildGatewayTestBinary();
    expect(existsSync(executable)).toBe(true);

    await cleanupGatewayTestBinary();

    expect(existsSync(executable)).toBe(false);
  }, 60_000);
});
