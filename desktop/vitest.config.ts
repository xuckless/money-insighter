import { resolve } from "node:path";

import { defineConfig } from "vitest/config";

// Unit tests for the renderer's pure modules (lib/) and for the packaging
// scripts. They run in Node: none of them touches the DOM or window.api.
export default defineConfig({
  resolve: {
    alias: {
      "@": resolve("src/renderer/src"),
      "@shared": resolve("src/shared"),
    },
  },
  test: {
    include: ["src/**/*.test.ts", "scripts/**/*.test.mjs"],
    environment: "node",
  },
});
