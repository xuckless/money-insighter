package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"plaidsync/internal/plaid"
)

// webhookMaxAge is how old a webhook's JWT may be. Plaid signs each
// delivery with a fresh iat; anything older is a replay or a very late
// retry and is refused.
const webhookMaxAge = 5 * time.Minute

// webhookClockSkew is the tolerance for an iat slightly in the future.
const webhookClockSkew = 30 * time.Second

// ErrWebhookUnverified wraps every verification failure so the handler
// can answer 401 without distinguishing causes to the caller.
var ErrWebhookUnverified = errors.New("webhook verification failed")

// webhookClaims are the claims of a Plaid webhook JWT: iat and the SHA-256
// of the request body.
type webhookClaims struct {
	jwt.RegisteredClaims
	RequestBodySHA256 string `json:"request_body_sha256"`
}

// Verifier checks the Plaid-Verification header of incoming webhooks: an
// ES256 JWT whose key is fetched from /webhook_verification_key/get by
// key id and cached, whose iat is recent, and whose request_body_sha256
// claim matches the body. Keys Plaid has marked expired are refused, as
// Plaid's reference implementation does.
type Verifier struct {
	plaid plaid.Client
	now   func() time.Time

	mu   sync.Mutex
	keys map[string]*cachedKey
}

type cachedKey struct {
	pub     *ecdsa.PublicKey
	expired bool
}

// NewVerifier builds a Verifier that resolves keys through pc.
func NewVerifier(pc plaid.Client) *Verifier {
	return &Verifier{plaid: pc, now: time.Now, keys: make(map[string]*cachedKey)}
}

// Verify checks token (the Plaid-Verification header) against body and
// returns nil when the webhook is authentic. Every failure wraps
// ErrWebhookUnverified.
func (v *Verifier) Verify(ctx context.Context, token string, body []byte) error {
	if token == "" {
		return fmt.Errorf("%w: missing Plaid-Verification header", ErrWebhookUnverified)
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuedAt(), jwt.WithLeeway(webhookClockSkew))
	unverified, _, err := parser.ParseUnverified(token, &webhookClaims{})
	if err != nil {
		return fmt.Errorf("%w: malformed token: %v", ErrWebhookUnverified, err)
	}
	if alg, _ := unverified.Header["alg"].(string); alg != jwt.SigningMethodES256.Alg() {
		return fmt.Errorf("%w: unexpected alg %q", ErrWebhookUnverified, alg)
	}
	kid, _ := unverified.Header["kid"].(string)
	if kid == "" {
		return fmt.Errorf("%w: token has no kid", ErrWebhookUnverified)
	}

	key, err := v.key(ctx, kid)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookUnverified, err)
	}
	if key.expired {
		return fmt.Errorf("%w: key %s has expired", ErrWebhookUnverified, kid)
	}

	claims := &webhookClaims{}
	_, err = parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) { return key.pub, nil })
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookUnverified, err)
	}
	if claims.IssuedAt == nil {
		return fmt.Errorf("%w: token has no iat", ErrWebhookUnverified)
	}
	if age := v.now().Sub(claims.IssuedAt.Time); age > webhookMaxAge {
		return fmt.Errorf("%w: token issued %s ago", ErrWebhookUnverified, age.Round(time.Second))
	}
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(want), []byte(claims.RequestBodySHA256)) != 1 {
		return fmt.Errorf("%w: body digest mismatch", ErrWebhookUnverified)
	}
	return nil
}

// key returns the cached key for kid, fetching it from Plaid on a miss.
// An unknown kid is fetched every time it is seen, which is what Plaid
// recommends; a kid Plaid does not know is an error and is not cached.
func (v *Verifier) key(ctx context.Context, kid string) (*cachedKey, error) {
	v.mu.Lock()
	if k, ok := v.keys[kid]; ok {
		v.mu.Unlock()
		return k, nil
	}
	v.mu.Unlock()

	vk, err := v.plaid.WebhookVerificationKey(ctx, kid)
	if err != nil {
		return nil, fmt.Errorf("fetch key %s: %w", kid, err)
	}
	pub, err := publicKeyFromJWK(vk)
	if err != nil {
		return nil, fmt.Errorf("key %s: %w", kid, err)
	}
	k := &cachedKey{pub: pub, expired: vk.ExpiredAt != nil}

	v.mu.Lock()
	v.keys[kid] = k
	v.mu.Unlock()
	return k, nil
}

// publicKeyFromJWK builds a P-256 public key from Plaid's JWK fields.
func publicKeyFromJWK(k *plaid.VerificationKey) (*ecdsa.PublicKey, error) {
	if k.KeyType != "EC" || k.Curve != "P-256" || (k.Algorithm != "" && k.Algorithm != "ES256") {
		return nil, fmt.Errorf("unsupported key type %s/%s/%s", k.KeyType, k.Curve, k.Algorithm)
	}
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("x: %w", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("y: %w", err)
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, errors.New("point is not on P-256")
	}
	return pub, nil
}
