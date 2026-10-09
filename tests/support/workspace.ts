import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { execFileSync } from "node:child_process";

/** `core/tests/support` -> `core`, so siblings resolve from the workspace root. */
const coreRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

/**
 * Resolves the workspace root that holds Core's sibling repositories. The
 * plugin-agnostic gate checks out only the Plugin SDK, so a scenario that spans
 * Core with real product plugins has to be able to state that it is out of scope
 * there instead of failing on a path that was never supposed to exist.
 */
export function workspaceRoot(): string {
  const override = process.env.LIAPOLDUS_WORKSPACE_ROOT;
  return override === undefined || override === "" ? dirname(coreRoot) : override;
}

/**
 * Names the sibling repositories a cross-repository scenario cannot run without.
 * Each entry is relative to {@link workspaceRoot}, mirroring the layout the
 * fixture and the product modules resolve for themselves.
 */
export function missingWorkspaceRepositories(...relative: string[]): string[] {
  return relative.filter((path) => !existsSync(join(workspaceRoot(), path)));
}

/**
 * Creates an isolated Go workspace for cross-repository tests. The workspace
 * file lives outside every checkout, so a test cannot mutate a user's source
 * tree or depend on a checked-in root go.work file.
 */
export function createGoWorkspace(...relativeModules: string[]): { path: string; cleanup: () => void } {
  const directory = mkdtempSync(join(tmpdir(), "liapoldus-go-work-"));
  const modules = relativeModules.map((module) => join(workspaceRoot(), module));
  try {
    execFileSync("go", ["work", "init", ...modules], {
      cwd: directory,
      env: { ...process.env, GOWORK: "off" },
      stdio: "pipe",
      timeout: 30_000,
    });
    execFileSync("go", ["work", "edit", "-go=1.26.0", join(directory, "go.work")], {
      cwd: directory,
      env: { ...process.env, GOWORK: "off" },
      stdio: "pipe",
      timeout: 30_000,
    });
  } catch (error) {
    rmSync(directory, { recursive: true, force: true });
    throw error;
  }
  return {
    path: join(directory, "go.work"),
    cleanup: () => rmSync(directory, { recursive: true, force: true }),
  };
}
