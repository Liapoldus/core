import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import { basename, join } from "node:path";

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exitCode = 1;
}

const directory = process.argv[2];
if (!directory || process.argv.length !== 3) {
  fail("contract bundle directory is required");
} else {
  try {
    const manifestPath = join(directory, "manifest.json");
    const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
    const declared = manifest?.files;
    if (!declared || typeof declared !== "object" || Array.isArray(declared) || Object.keys(declared).length === 0) {
      throw new Error("contract manifest has no payload files");
    }

    const entries = await readdir(directory, { withFileTypes: true });
    const payloads = entries.filter((entry) => entry.name !== basename(manifestPath));
    const declaredNames = Object.keys(declared).sort();
    const actualNames = payloads.map((entry) => entry.name).sort();
    const undeclared = actualNames.filter((name) => !Object.hasOwn(declared, name));
    if (undeclared.length > 0) {
      throw new Error(`unmanifested contract payload: ${undeclared.join(", ")}`);
    }
    const missing = declaredNames.filter((name) => !actualNames.includes(name));
    if (missing.length > 0) {
      throw new Error(`missing contract payload: ${missing.join(", ")}`);
    }

    for (const name of declaredNames) {
      if (basename(name) !== name || name === basename(manifestPath)) {
        throw new Error(`invalid contract payload name: ${name}`);
      }
      const entry = payloads.find((candidate) => candidate.name === name);
      if (!entry?.isFile()) {
        throw new Error(`contract payload is not a regular file: ${name}`);
      }
      const expected = declared[name];
      if (typeof expected !== "string" || !/^sha256:[0-9a-f]{64}$/.test(expected)) {
        throw new Error(`invalid contract payload digest: ${name}`);
      }
      const digest = createHash("sha256").update(await readFile(join(directory, name))).digest("hex");
      if (`sha256:${digest}` !== expected) {
        throw new Error(`contract payload digest mismatch: ${name}`);
      }
    }
  } catch (error) {
    fail(error instanceof Error ? error.message : "contract bundle is invalid");
  }
}
