import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source";

const root = join(import.meta.dirname, "../..");

describe("approved orphan symbol cleanup", () => {
  it("removes only the five confirmed unreferenced symbols", async () => {
    const [api, services, runtime] = await Promise.all([
      readGoPackageSources(root, "internal/presentation/api"),
      readGoPackageSources(root, "internal/infrastructure/services"),
      readGoPackageSources(root, "internal/runtime"),
    ]);

    expect(api).not.toMatch(/func redact\(/);
    expect(services).not.toMatch(/func \(c \*Client\) CallJSON\(/);
    expect(runtime).toMatch(/func Serve\(/);
    expect(services).not.toMatch(/func \(r \*Runtime\) HTTPDispatchers\(/);
    expect(services).not.toMatch(/func \(r \*Runtime\) L4Dispatchers\(/);
  });
});
