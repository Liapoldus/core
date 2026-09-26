import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const testDirectory = import.meta.dirname;

async function engines(path: string): Promise<string | undefined> {
  const document = JSON.parse(await readFile(path, "utf8")) as {
    engines?: { node?: string };
  };
  return document.engines?.node;
}

describe("Node runtime requirement", () => {
  it("declares Node.js 22 or newer for the Gateway test runner", async () => {
    await expect(engines(join(testDirectory, "../../package.json"))).resolves.toBe(">=22");
    await expect(engines(join(testDirectory, "../package.json"))).resolves.toBe(">=22");
  });
});
