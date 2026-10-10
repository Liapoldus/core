import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source.js";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const maximumFunctionLines = 80;

function measuredFunctionLines(source: string, name: string): number {
  const declarations = [...source.matchAll(/^func(?:\s+\([^)]*\))?\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(/gm)];
  const index = declarations.findIndex((declaration) => declaration[1] === name);
  if (index < 0) throw new Error(`function ${name} was not found`);

  const start = declarations[index].index ?? 0;
  const end = declarations[index + 1]?.index ?? source.length;
  const lines = source.slice(start, end).split("\n");
  while (lines.at(-1)?.trim() === "") lines.pop();
  return lines.length;
}

describe("presentation function decomposition budget", () => {
  it.each([
    ["api", "handle"],
    ["../runtime", "Serve"],
  ])("keeps %s.%s at or below the agreed function budget", async (packageName, functionName) => {
    const source = await readGoPackageSources(
      root,
      packageName === "api" ? "internal/presentation/api" : "internal/runtime",
    );

    expect(measuredFunctionLines(source, functionName)).toBeLessThanOrEqual(maximumFunctionLines);
  });
});
