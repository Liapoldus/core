import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

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