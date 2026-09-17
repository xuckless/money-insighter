package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"plaidsync/internal/secret"
)

// decodeKEK decodes a base64-encoded key encryption key and checks that it
// is exactly kekBytes long. Standard base64 is accepted with or without
// padding. On failure the returned reason is fixed text that never includes
// the input; the decoder's own error is dropped for the same reason.
//
// This deliberately duplicates the small decode in internal/crypto rather
// than importing it, so the config package stays a leaf that depends only on
// internal/secret.
func decodeKEK(b64 string) (secret.Bytes, string) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			return secret.Bytes{}, "must be base64"
		}
	}
	defer clear(raw)
	if len(raw) != kekBytes {
		return secret.Bytes{}, fmt.Sprintf("must be base64 of exactly %d bytes (decoded %d)", kekBytes, len(raw))
	}
	return secret.NewBytes(raw), ""
}

// keks builds the keyring map: PLAIDSYNC_KEK at the active version plus
// every "version:base64" entry from PLAIDSYNC_KEK_PREVIOUS. Problems are
// recorded on r; the returned map holds whatever decoded cleanly.
func (r *reader) keks(active uint32) map[uint32]secret.Bytes {
	out := make(map[uint32]secret.Bytes)

	if v, ok := r.required(envKEK); ok {
		key, reason := decodeKEK(v)
		if reason != "" {
			r.fail(envKEK, reason)
		} else {
			out[active] = key
		}
	}

	for i, entry := range r.list(envKEKPrevious, nil) {
		n := i + 1 // 1-based position for humans
		verText, b64, found := strings.Cut(entry, ":")
		if !found {
			r.failf(envKEKPrevious, "entry %d: must be version:base64", n)
			continue
		}
		ver, err := strconv.ParseUint(strings.TrimSpace(verText), 10, 32)
		if err != nil || ver == 0 {
			r.failf(envKEKPrevious, "entry %d: version must be a positive integer", n)
			continue
		}
		version := uint32(ver)
		if version == active {
			r.failf(envKEKPrevious, "entry %d: version %d is the active version (%s); previous versions must differ", n, version, envKEKVersion)
			continue
		}
		if _, dup := out[version]; dup {
			r.failf(envKEKPrevious, "entry %d: version %d is listed more than once", n, version)
			continue
		}
		key, reason := decodeKEK(strings.TrimSpace(b64))
		if reason != "" {
			r.failf(envKEKPrevious, "entry %d: key %s", n, reason)
			continue
		}
		out[version] = key
	}
	return out
}
