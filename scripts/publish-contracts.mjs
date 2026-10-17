import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const sourceDirectory = join(root, "assets/contracts");
const outputDirectory = join(root, "contracts/v1");
const publication = JSON.parse(await readFile(join(sourceDirectory, "publication.json"), "utf8"));
const manifestPath = join(outputDirectory, "manifest.json");
const manifest = JSON.parse(await readFile(manifestPath, "utf8"));

if (
  !Array.isArray(publication.files) ||
  publication.files.length === 0 ||
  typeof manifest.files !== "object" ||
  manifest.files === null ||
  Array.isArray(manifest.files)
) {
  throw new Error("contract publication list must contain at least one file");
}

const sources = new Set();
const targets = new Set();
const publications = [];
for (const file of publication.files) {
  if (
    typeof file.source !== "string" ||
    file.source === "." ||
    file.source === ".." ||
    basename(file.source) !== file.source
  ) {
    throw new Error("contract publication source must be a file name");
  }
  if (
    typeof file.target !== "string" ||
    file.target === "." ||
    file.target === ".." ||
    basename(file.target) !== file.target
  ) {
    throw new Error("contract publication target must be a file name");
  }
  if (sources.has(file.source) || targets.has(file.target)) {
    throw new Error("contract publication file names must be unique");
  }
  if (!Object.hasOwn(manifest.files, file.target)) {
    throw new Error(`contract publication target is absent from manifest: ${file.target}`);
  }
  sources.add(file.source);
  targets.add(file.target);

  const contents = await readFile(join(sourceDirectory, file.source));
  manifest.files[file.target] = `sha256:${createHash("sha256").update(contents).digest("hex")}`;
  publications.push({ target: file.target, contents });
}

for (const target of Object.keys(manifest.files)) {
  if (!targets.has(target)) await readFile(join(outputDirectory, target));
}
for (const publication of publications) {
  await writeFile(join(outputDirectory, publication.target), publication.contents);
}
for (const target of Object.keys(manifest.files)) {
  const contents = await readFile(join(outputDirectory, target));
  manifest.files[target] = `sha256:${createHash("sha256").update(contents).digest("hex")}`;
}
await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);

// v2 traffic fields have the same Core owner; publish their mirror and
// regenerate all digests from the final canonical contract bytes.
const v2Directory = join(root, "contracts/v2");
await writeFile(join(v2Directory, "traffic-rollout-fields.yaml"),
  await readFile(join(sourceDirectory, "v2/traffic-rollout-fields.yaml")));
const v2ManifestPath = join(v2Directory, "manifest.json");
const v2Manifest = JSON.parse(await readFile(v2ManifestPath, "utf8"));
for (const target of Object.keys(v2Manifest.files)) {
  if (basename(target) !== target) throw new Error("invalid v2 contract file name");
  const contents = await readFile(join(v2Directory, target));
  v2Manifest.files[target] = `sha256:${createHash("sha256").update(contents).digest("hex")}`;
}
await writeFile(v2ManifestPath, `${JSON.stringify(v2Manifest, null, 2)}\n`);
