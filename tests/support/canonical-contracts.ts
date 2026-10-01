import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** `core/tests/support` -> `core`, so the docs repo is found as a workspace sibling. */
const coreRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

const docsRepo = "Liapoldus/liapoldus.github.io";
const docsBranch = "main";

/** Runs git without a shell and reports failure as "not usable" instead of throwing. */
function git(cwd: string, ...args: string[]): string | null {
  try {
    return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return null;
  }
}

/** Read the tracked remote revision, never uncommitted docs working files. */
function canonicalLocalFile(candidate: string, relativePath: string): Buffer | null {
  const remote = git(candidate, "remote", "get-url", "origin");
  if (remote !== `https://github.com/${docsRepo}.git` && remote !== `git@github.com:${docsRepo}.git`) {
    return null;
  }
  try {
    return execFileSync("git", ["show", `origin/${docsBranch}:${relativePath}`], {
      cwd: candidate,
      stdio: ["ignore", "pipe", "ignore"],
    });
  } catch {
    return null;
  }
}

function checkoutCandidates(): string[] {
  const override = process.env.LIAPOLDUS_DOCS_CHECKOUT;
  return override === undefined || override === ""
    ? [join(dirname(coreRoot), "liapoldus.github.io")]
    : [override];
}

/**
 * Loads one canonical documentation file. Resolution order is a local
 * origin/main tracking revision first and the GitHub REST API second. The
 * working tree is never read, so unrelated dirty docs cannot alter the gate.
 */
export async function loadCanonicalDocsFile(relativePath: string): Promise<Buffer> {
  for (const candidate of checkoutCandidates()) {
    const local = canonicalLocalFile(candidate, relativePath);
    if (local !== null) {
      return local;
    }
  }

  const url = `https://api.github.com/repos/${docsRepo}/contents/${relativePath}?ref=${docsBranch}`;
  let response: Response;
  try {
    response = await fetch(url, {
      headers: { Accept: "application/vnd.github.raw+json" },
      signal: AbortSignal.timeout(10_000),
    });
  } catch (cause) {
    throw new Error(
      `canonical Core contract "${relativePath}" is unavailable: no verified local checkout and ${url} could not be reached (${String(cause)})`,
    );
  }
  if (!response.ok) {
    throw new Error(
      `canonical Core contract "${relativePath}" is unavailable: no verified local checkout and ${url} returned ${response.status} ${response.statusText}`,
    );
  }
  return Buffer.from(await response.arrayBuffer());
}
