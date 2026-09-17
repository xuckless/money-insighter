package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"plaidsync/internal/plaid"
	"plaidsync/internal/plaid/plaidtest"
)

// signer is a test ECDSA key registered with the fake Plaid as a webhook
// verification key.
type signer struct {
	priv *ecdsa.PrivateKey
	kid  string
}

func newSigner(t *testing.T, f *plaidtest.Fake, kid string, expired bool) *signer {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pad := func(b []byte) []byte {
		out := make([]byte, 32)
		copy(out[32-len(b):], b)
		return out
	}
	key := &plaid.VerificationKey{
		KeyID: kid, Algorithm: "ES256", Curve: "P-256", KeyType: "EC", Use: "sig",
		X:         base64.RawURLEncoding.EncodeToString(pad(priv.PublicKey.X.Bytes())),
		Y:         base64.RawURLEncoding.EncodeToString(pad(priv.PublicKey.Y.Bytes())),
		CreatedAt: time.Now().Add(-time.Hour),
	}
	if expired {
		e := time.Now().Add(-time.Minute)
		key.ExpiredAt = &e
	}
	f.AddKey(key)
	return &signer{priv: priv, kid: kid}
}

// sign produces a Plaid-Verification header for body issued at iat.
func (s *signer) sign(t *testing.T, body []byte, iat time.Time) string {
	t.Helper()
	sum := sha256.Sum256(body)
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, webhookClaims{
		RegisteredClaims:  jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(iat)},
		RequestBodySHA256: hex.EncodeToString(sum[:]),
	})
	tok.Header["kid"] = s.kid
	out, err := tok.SignedString(s.priv)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifierAcceptsValidWebhook(t *testing.T) {
	f := plaidtest.New()
	s := newSigner(t, f, "kid-1", false)
	v := NewVerifier(f)
	body := []byte(`{"webhook_type":"TRANSACTIONS","webhook_code":"SYNC_UPDATES_AVAILABLE","item_id":"i"}`)

	if err := v.Verify(context.Background(), s.sign(t, body, time.Now()), body); err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	// The key is cached: a second verification makes no Plaid call.
	before := len(f.CallsTo(plaidtest.OpWebhookVerificationKey))
	if err := v.Verify(context.Background(), s.sign(t, body, time.Now()), body); err != nil {
		t.Fatal(err)
	}
	if after := len(f.CallsTo(plaidtest.OpWebhookVerificationKey)); after != before || before != 1 {
		t.Errorf("key fetches = %d then %d, want 1 and 1", before, after)
	}
}

func TestVerifierRejects(t *testing.T) {
	f := plaidtest.New()
	s := newSigner(t, f, "kid-1", false)
	expired := newSigner(t, f, "kid-old", true)
	v := NewVerifier(f)
	body := []byte(`{"webhook_type":"ITEM","webhook_code":"ERROR"}`)
	ctx := context.Background()

	cases := map[string]struct {
		header string
		body   []byte
	}{
		"missing header":  {"", body},
		"garbage":         {"not.a.jwt", body},
		"body mismatch":   {s.sign(t, body, time.Now()), []byte(`{"webhook_type":"ITEM","webhook_code":"ERROR","x":1}`)},
		"too old":         {s.sign(t, body, time.Now().Add(-10*time.Minute)), body},
		"from the future": {s.sign(t, body, time.Now().Add(5*time.Minute)), body},
		"expired key":     {expired.sign(t, body, time.Now()), body},
		"unknown key":     {(&signer{priv: s.priv, kid: "kid-unknown"}).sign(t, body, time.Now()), body},
	}
	for name, c := range cases {
		err := v.Verify(ctx, c.header, c.body)
		if !errors.Is(err, ErrWebhookUnverified) {
			t.Errorf("%s: err = %v, want ErrWebhookUnverified", name, err)
		}
	}

	// A token signed by a different key under a known kid.
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	forged := (&signer{priv: other, kid: "kid-1"}).sign(t, body, time.Now())
	if err := v.Verify(ctx, forged, body); !errors.Is(err, ErrWebhookUnverified) {
		t.Errorf("forged signature: %v", err)
	}

	// HS256 with the public key as the secret (the classic confusion
	// attack) must be refused by the method allowlist.
	sum := sha256.Sum256(body)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, webhookClaims{
		RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(time.Now())}, RequestBodySHA256: hex.EncodeToString(sum[:])})
	hs.Header["kid"] = "kid-1"
	hsToken, _ := hs.SignedString([]byte("whatever"))
	if err := v.Verify(ctx, hsToken, body); !errors.Is(err, ErrWebhookUnverified) {
		t.Errorf("HS256 token: %v", err)
	}
}
