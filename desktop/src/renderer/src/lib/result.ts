// Result is what mutations return: failures as values, with a sentence the
// UI can show.
export type Result<T> = { ok: true; data: T } | { ok: false; error: string };
