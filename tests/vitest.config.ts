import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["unit/**/*.test.ts", "integration/**/*.test.ts", "architecture/**/*.test.ts"],
    testTimeout: 15_000,
    hookTimeout: 15_000,
    fileParallelism: false,
  },
});
