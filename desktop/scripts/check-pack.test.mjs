// check-pack.mjs only ever runs on a macOS packaging host, so these build
// the .app trees it inspects by hand.
import { chmodSync, mkdirSync, symlinkSync, writeFileSync, rmSync } from "node:fs";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import checkPack from "./check-pack.mjs";

const dirs = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

// bundle writes a well-formed darwin-arm64 .app and returns an afterPack
// context pointing at it. Each option knocks out one thing the hook checks.
function bundle({ postgres = true, symlinks = true, goMode = 0o755 } = {}) {
  const out = mkdtempSync(join(tmpdir(), "check-pack-"));
  dirs.push(out);
  const resources = join(out, "Money Insighter.app", "Contents", "Resources");

  mkdirSync(join(resources, "bin"), { recursive: true });
  for (const name of ["plaidsync", "topper"]) {
    const bin = join(resources, "bin", name);
    writeFileSync(bin, "#!/bin/sh\n");
    chmodSync(bin, goMode);
  }

  if (postgres) {
    const native = join(resources, "app", "node_modules", "@embedded-postgres", "darwin-arm64", "native");
    mkdirSync(join(native, "bin"), { recursive: true });
    mkdirSync(join(native, "lib"), { recursive: true });
    writeFileSync(join(native, "bin", "postgres"), "");
    writeFileSync(join(native, "lib", "libzstd.1.5.6.dylib"), "");
    if (symlinks) symlinkSync("libzstd.1.5.6.dylib", join(native, "lib", "libzstd.1.dylib"));
  }

  return {
    electronPlatformName: "darwin",
    arch: 3, // electron-builder's Arch.arm64
    appOutDir: out,
    packager: { appInfo: { productFilename: "Money Insighter" } },
  };
}

describe("check-pack", () => {
  it("passes a well-formed bundle", async () => {
    await expect(checkPack(bundle())).resolves.toBeUndefined();
  });

  it("ignores everything that is not macOS", async () => {
    const ctx = bundle({ postgres: false });
    await expect(checkPack({ ...ctx, electronPlatformName: "linux" })).resolves.toBeUndefined();
  });

  it("catches a missing Postgres tree for the target arch", async () => {
    await expect(checkPack(bundle({ postgres: false }))).rejects.toThrow(/@embedded-postgres\/darwin-arm64 is missing/);
  });

  it("catches unhydrated dylib symlinks", async () => {
    await expect(checkPack(bundle({ symlinks: false }))).rejects.toThrow(/no symlinks in/);
  });

  it("catches a Go service that lost its executable bit", async () => {
    await expect(checkPack(bundle({ goMode: 0o644 }))).rejects.toThrow(/plaidsync is not executable/);
  });
});
