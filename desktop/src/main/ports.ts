import { createServer } from "node:net";

// freePort asks the OS for an unused loopback TCP port. There is a window
// between closing the probe and the service binding it, which is
// acceptable for a single-user desktop.
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (!addr || typeof addr === "string") {
        srv.close();
        reject(new Error("could not allocate a port"));
        return;
      }
      srv.close(() => resolve(addr.port));
    });
  });
}
