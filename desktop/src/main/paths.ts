import { app } from "electron";
import { join } from "node:path";

// Everything the app writes lives under Electron's userData directory:
//   config.json     non-secret settings
//   secrets.bin     safeStorage-encrypted secrets
//   postgres/       the database cluster
//   logs/           one file per service
export function dataDir(): string {
  return app.getPath("userData");
}

export const paths = {
  config: () => join(dataDir(), "config.json"),
  secrets: () => join(dataDir(), "secrets.bin"),
  postgres: () => join(dataDir(), "postgres"),
  logs: () => join(dataDir(), "logs"),
};

// hostTarget is the resources/bin/<os>-<arch> directory name for this
// machine, matching scripts/build-go.mjs and electron-builder's ${os}-${arch}.
export function hostTarget(): string {
  const os = { linux: "linux", win32: "win", darwin: "mac" }[process.platform as string];
  const arch = { x64: "x64", arm64: "arm64" }[process.arch as string];
  if (!os || !arch) throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
  return `${os}-${arch}`;
}

// serviceBinary locates a Go service: next to the app's resources when
// packaged, under resources/bin/<host> in development.
export function serviceBinary(name: "plaidsync" | "topper"): string {
  const file = process.platform === "win32" ? `${name}.exe` : name;
  if (app.isPackaged) return join(process.resourcesPath, "bin", file);
  return join(app.getAppPath(), "resources", "bin", hostTarget(), file);
}
