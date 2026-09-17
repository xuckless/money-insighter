// Installs the embedded Postgres binaries for a platform other than the
// host, for cross-packaging (electron-builder --win on Linux, say). npm
// skips @embedded-postgres/<platform> packages whose "os" does not match
// the host, so this fetches the tarball with `npm pack`, extracts it into
// node_modules and re-creates the symlinks the package's postinstall would.
//
//   node scripts/fetch-postgres.mjs windows-x64
//   node scripts/fetch-postgres.mjs darwin-arm64 darwin-x64
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const pkgJson = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
const version = pkgJson.dependencies["embedded-postgres"];
if (!version || version.startsWith("^") || version.startsWith("~")) {
  throw new Error("pin embedded-postgres to an exact version in package.json");
}

const targets = process.argv.slice(2);
if (targets.length === 0) {
  console.error("usage: node scripts/fetch-postgres.mjs <platform-arch>...  (windows-x64, darwin-arm64, darwin-x64, linux-x64)");
  process.exit(2);
}

for (const target of targets) {
  const name = `@embedded-postgres/${target}`;
  const dest = join(root, "node_modules", "@embedded-postgres", target);
  if (existsSync(join(dest, "package.json"))) {
    const have = JSON.parse(readFileSync(join(dest, "package.json"), "utf8")).version;
    if (have === version) {
      console.log(`${name}@${version} already present`);
      continue;
    }
    rmSync(dest, { recursive: true, force: true });
  }
  const tmp = mkdtempSync(join(tmpdir(), "embedded-postgres-"));
  console.log(`fetching ${name}@${version}`);
  const out = execFileSync("npm", ["pack", `${name}@${version}`, "--pack-destination", tmp, "--silent"], { encoding: "utf8" }).trim();
  const tarball = join(tmp, out.split("\n").pop());
  mkdirSync(dest, { recursive: true });
  execFileSync("tar", ["xzf", tarball, "--strip-components=1", "-C", dest], { stdio: "inherit" });
  rmSync(tmp, { recursive: true, force: true });
  const hydrate = join(dest, "scripts", "hydrate-symlinks.js");
  if (existsSync(hydrate)) execFileSync(process.execPath, [hydrate], { cwd: dest, stdio: "inherit" });
  console.log(`installed ${name}@${version} into ${dest}`);
}
