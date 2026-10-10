import { afterAll } from "vitest";
import { createGoWorkspace } from "./workspace.js";

// Most fixture tests intentionally exercise Core in isolation and inherit the
// test process environment. Once Core consumes the v3 SDK module, those child
// Go commands still need the coordinated local modules until the release tags
// exist. Keep one generated workspace for that default path; tests that need a
// different module set continue to pass their own GOWORK explicitly.
const workspace = createGoWorkspace("core", "plugin-sdk", "pluginprotocol");
process.env.GOWORK = workspace.path;

afterAll(() => workspace.cleanup());
