// Package secret provides value types that make accidental leakage of
// sensitive strings and bytes through formatting, logging, and JSON
// structurally hard.
//
// [Token] wraps a sensitive string (Plaid access token, public token, API
// secret, database URL) and [Bytes] wraps a sensitive byte slice (the key
// encryption key). Both render as [Redacted] under every fmt verb, when
// marshalled to JSON or text, and when logged through log/slog. The only way
// to read the underlying value is the Expose method, which makes every read
// site greppable.
//
// # Reflective formatting
//
// fmt only calls String, GoString, or Format on values it can obtain through
// reflect.Value.Interface. A Token or Bytes stored in an unexported struct
// field and printed with %v or %+v is instead walked reflectively and its
// internals are printed raw. To make even that path harmless, the stored
// value is XOR-masked with a process-random pad generated once from
// crypto/rand at package initialisation; reflective formatting therefore
// shows only meaningless bytes.
//
// The masking is deliberately not a security boundary: the pad lives in the
// same process, so anyone who can read the process memory can recover the
// plaintext. Its sole purpose is to keep secrets out of logs, error strings,
// and panic output produced by reflective formatting.
package secret

// Redacted is the placeholder that [Token] and [Bytes] render in place of
// their value under every fmt verb, in JSON and text marshalling, and in
// log/slog output.
const Redacted = "[REDACTED]"
