import type { HttpResult } from "@shared/api";

// proxy performs one request against a local service with its bearer
// token and returns the status and parsed body. Transport failures become
// a 503 with a message body, which is what the renderer's clients expect.
export async function proxy(base: string, token: string, method: string, path: string, body?: unknown): Promise<HttpResult> {
  let res: Response;
  try {
    res = await fetch(base + path, {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(60_000),
    });
  } catch (err) {
    return { status: 503, body: { error: `service unreachable: ${(err as Error).message}` } };
  }
  const text = await res.text();
  let parsed: unknown;
  try {
    parsed = text ? JSON.parse(text) : undefined;
  } catch {
    parsed = { error: text.trim() || res.statusText };
  }
  return { status: res.status, body: parsed };
}
