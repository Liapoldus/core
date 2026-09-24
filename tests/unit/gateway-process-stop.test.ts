import { spawn } from "node:child_process";
import { once } from "node:events";
import { afterEach, describe, expect, it } from "vitest";
import { observeChildClose, stopChildProcess } from "../support/gateway.js";

const children: ReturnType<typeof spawn>[] = [];

afterEach(() => {
  for (const child of children.splice(0)) {
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  }
});

describe("Gateway child-process cleanup", () => {
  it("resolves when close happened before cleanup requests shutdown", async () => {
    const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
    children.push(child);
    const closed = observeChildClose(child);
    await once(child, "spawn");
    child.kill("SIGTERM");
    await closed;

    await expect(stopChildProcess(child, closed)).resolves.toBeUndefined();
  });
});
