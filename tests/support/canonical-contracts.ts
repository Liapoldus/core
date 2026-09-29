import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
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

/**
 * A local docs checkout may only be trusted while it is a real git repository
 * whose working HEAD is exactly the fetched `origin/main`. Anything else (a
 * non-repository, a detached head, a branch that drifted) is ignored so a stale
 * working tree can never masquerade as the canonical contract.
 */
function isAuthoritativeCheckout(candidate: string): boolean {
  const head = git(candidate, "rev-parse", "HEAD");
  const originMain = git(candidate, "rev-parse", `origin/${docsBranch}`);
  return head !== null && originMain !== null && head === originMain;
}

function checkoutCandidates(): string[] {
  const override = process.env.LIAPOLDUS_DOCS_CHECKOUT;
  return override === undefined || override === ""
    ? [join(dirname(coreRoot), "liapoldus.github.io")]
    : [override];
}

/**
 * Loads one canonical documentation file. Resolution order is a verified local
 * checkout first and the GitHub REST API second; if neither yields the file the
 * call rejects. There is deliberately no untracked fallback: a missing canonical
 * contract must fail verification rather than be silently replaced by a copy
 * that is not the published one.
 */
export async function loadCanonicalDocsFile(relativePath: string): Promise<Buffer> {
  for (const candidate of checkoutCandidates()) {
    if (!isAuthoritativeCheckout(candidate)) {
      continue;
    }
    try {
      return readFileSync(join(candidate, relativePath));
    } catch {
      // A verified checkout that lacks the file still falls through to the API.
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
