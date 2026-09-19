// electron-builder afterPack hook. Runs on the packed .app before it is
// signed, and throws on the three ways this app can produce a bundle that
// signs, notarizes and staples perfectly and then dies on first launch:
//
//   1. the @embedded-postgres dylib symlinks were never hydrated, so
//      postgres fails with "Library not loaded: @loader_path/../lib/...";
//   2. a Go service lost its executable bit, so spawn() fails with EACCES;
//   3. the Postgres package for the target arch was left out of the bundle
//      (electron-builder resolves node_modules through `npm list --json`,
//      which does not necessarily see a hand-extracted cross-arch tree).
//
// None of this is signing: electron-builder's own walk signs every nested
// Mach-O file, including these. This only checks that they are there.
import { readdirSync, statSync, lstatSync, existsSync } from "node:fs";
import { join } from "node:path";

export default async function checkPack(context) {
  if (context.electronPlatformName !== "darwin") return;

  const arch = { 0: "ia32", 1: "x64", 3: "arm64" }[context.arch] ?? String(context.arch);
  const app = join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`);
  const resources = join(app, "Contents", "Resources");
  const problems = [];

  // 1 + 3: the Postgres tree for this arch, with its dylib symlinks intact.
  const pg = join(resources, "app", "node_modules", "@embedded-postgres", `darwin-${arch}`);
  if (!existsSync(join(pg, "native", "bin", "postgres"))) {
    problems.push(
      `@embedded-postgres/darwin-${arch} is missing from the bundle (looked for ${pg}/native/bin/postgres).\n` +
        `    Install it for this arch before packaging:\n` +
        `      npm i --no-save --os=darwin --cpu=${arch} @embedded-postgres/darwin-${arch}@<exact version>\n` +
        `      (or: node scripts/fetch-postgres.mjs darwin-${arch})`,
    );
  } else {
    const lib = join(pg, "native", "lib");
    const links = readdirSync(lib).filter((f) => lstatSync(join(lib, f)).isSymbolicLink());
    if (links.length === 0) {
      problems.push(
        `no symlinks in ${lib}.\n` +
          `    The package's hydrate-symlinks.js postinstall did not run, so the\n` +
          `    dylib aliases postgres loads through @loader_path/../lib are absent.\n` +
          `    Reinstall without --ignore-scripts.`,
      );
    }
  }

  // 2: the Go services, present and executable.
  for (const name of ["plaidsync", "topper"]) {
    const bin = join(resources, "bin", name);
    if (!existsSync(bin)) {
      problems.push(`${name} is missing from the bundle (looked for ${bin}).`);
      continue;
    }
    const mode = statSync(bin).mode & 0o777;
    if ((mode & 0o111) === 0) {
      problems.push(`${bin} is not executable (mode ${mode.toString(8)}); spawn() will fail with EACCES.`);
    }
  }

  if (problems.length > 0) {
    throw new Error(`the packed app is not fit to sign:\n\n  - ${problems.join("\n\n  - ")}\n`);
  }
  console.log(`  • check-pack: darwin-${arch} bundle looks sane`);
}
