// Cross-compiles plaidsync and the topper into resources/bin/<os>-<arch>/,
// where electron-builder's extraResources picks up the current target and
// `electron-vite dev` finds the host's build.
//
//   node scripts/build-go.mjs            host platform only
//   node scripts/build-go.mjs --os windows [--arch x64]
//   node scripts/build-go.mjs --all      linux-x64, win32-x64, darwin-x64, darwin-arm64
import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, "..", "..");
const out = resolve(here, "..", "resources", "bin");

const services = [
  { name: "plaidsync", dir: "plaid-backend-golang", pkg: "./cmd/plaidsync" },
  { name: "topper", dir: "postgres-topper", pkg: "./cmd/topper" },
];

// electron-builder's ${os} is linux | win | mac; its ${arch} is x64 | arm64.
const targets = {
  "linux-x64": { GOOS: "linux", GOARCH: "amd64" },
  "win-x64": { GOOS: "windows", GOARCH: "amd64" },
  "mac-x64": { GOOS: "darwin", GOARCH: "amd64" },
  "mac-arm64": { GOOS: "darwin", GOARCH: "arm64" },
};

function hostTarget() {
  const os = { linux: "linux", win32: "win", darwin: "mac" }[process.platform];
  const arch = { x64: "x64", arm64: "arm64" }[process.arch];
  if (!os || !arch) throw new Error(`unsupported host ${process.platform}/${process.arch}`);
  return `${os}-${arch}`;
}

const args = process.argv.slice(2);
let wanted;
if (args.includes("--all")) {
  wanted = Object.keys(targets);
} else if (args.includes("--os")) {
  const os = { linux: "linux", windows: "win", win: "win", darwin: "mac", mac: "mac" }[args[args.indexOf("--os") + 1]];
  const arch = args.includes("--arch") ? args[args.indexOf("--arch") + 1] : "x64";
  wanted = [`${os}-${arch}`];
} else {
  wanted = [hostTarget()];
}

for (const target of wanted) {
  const env = targets[target];
  if (!env) throw new Error(`unknown target ${target}`);
  const dir = resolve(out, target);
  mkdirSync(dir, { recursive: true });
  for (const s of services) {
    const bin = resolve(dir, s.name + (env.GOOS === "windows" ? ".exe" : ""));
    process.stdout.write(`building ${s.name} for ${target} -> ${bin}\n`);
    execFileSync("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", bin, s.pkg], {
      cwd: resolve(repo, s.dir),
      env: { ...process.env, ...env, CGO_ENABLED: "0" },
      stdio: "inherit",
    });
  }
}
