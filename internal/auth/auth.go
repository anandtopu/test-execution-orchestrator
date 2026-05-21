// Package auth provides JWT issuance/verification and API-key hashing per ADR-0014.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

// Role values, mirroring teo.user_roles.role check constraint.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleEngineer Role = "engineer"
	RoleReadOnly Role = "read_only"
)

// Principal is the authenticated identity attached to a request context.
type Principal struct {
	UserID   string
	Email    string
	APIKeyID string
	Roles    []Role
	Scopes   []string
	IsAPIKey bool
}

// HasRole returns true if any of want is held by p.
func (p *Principal) HasRole(want ...Role) bool {
	for _, r := range p.Roles {
		if slices.Contains(want, r) {
			return true
		}
	}
	return false
}

// HasScope returns true if scope is held by p.
func (p *Principal) HasScope(scope string) bool {
	return slices.Contains(p.Scopes, scope)
}

type principalKey struct{}

// WithPrincipal returns a new context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal bound to ctx, or nil.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// --- JWT issuance ----------------------------------------------------------

// JWTIssuer signs and verifies short-lived bearer tokens (HS256 v1 per ADR-0014).
type JWTIssuer struct {
	Secret []byte
	TTL    time.Duration
	Issuer string
}

// Claims wraps standard registered claims with TEO additions.
type Claims struct {
	jwt.RegisteredClaims
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

// Issue creates a new JWT for the user.
func (j *JWTIssuer) Issue(userID, email string, roles []Role) (string, error) {
	now := time.Now()
	roleStrs := make([]string, len(roles))
	for i, r := range roles {
		roleStrs[i] = string(r)
	}
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.Issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.TTL)),
		},
		Email: email,
		Roles: roleStrs,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(j.Secret)
}

// Verify parses and validates a JWT, returning the embedded principal.
func (j *JWTIssuer) Verify(raw string) (*Principal, error) {
	tok, err := jwt.ParseWithClaims(raw, &Claims{}, func(_ *jwt.Token) (any, error) {
		return j.Secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	c, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	roles := make([]Role, len(c.Roles))
	for i, r := range c.Roles {
		roles[i] = Role(r)
	}
	return &Principal{
		UserID: c.Subject,
		Email:  c.Email,
		Roles:  roles,
	}, nil
}

// --- API keys --------------------------------------------------------------

// argon2 parameters per ADR-0014.
const (
	argonMemory      uint32 = 64 * 1024
	argonIterations  uint32 = 3
	argonParallelism uint8  = 1
	argonSaltLen     int    = 16
	argonKeyLen      uint32 = 32
)

// GenerateAPIKey creates a new key with the given prefix tag, returning
// (display, prefix, hash). The display value is shown to the user once and
// never persisted; only prefix + hash hit the database.
func GenerateAPIKey(tag string) (display, prefix, hash string, err error) {
	if tag == "" {
		tag = "ci"
	}
	secretBytes := make([]byte, 24)
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", "", err
	}
	pfxBytes := make([]byte, 6)
	if _, err = rand.Read(pfxBytes); err != nil {
		return "", "", "", err
	}
	prefix = fmt.Sprintf("teo_%s_%s", tag, hex.EncodeToString(pfxBytes))
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	display = prefix + "." + secret

	salt := make([]byte, argonSaltLen)
	if _, err = rand.Read(salt); err != nil {
		return "", "", "", err
	}
	derived := argon2.IDKey([]byte(secret), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	hash = encodeArgon(salt, derived)
	return display, prefix, hash, nil
}

// VerifyAPIKey checks that display matches stored prefix+hash; returns the prefix
// on success.
func VerifyAPIKey(display, expectedHash string) (string, bool) {
	dot := strings.LastIndexByte(display, '.')
	if dot <= 0 {
		return "", false
	}
	prefix := display[:dot]
	secret := display[dot+1:]

	salt, derived, err := decodeArgon(expectedHash)
	if err != nil {
		return "", false
	}
	got := argon2.IDKey([]byte(secret), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	if subtle.ConstantTimeCompare(got, derived) != 1 {
		return "", false
	}
	return prefix, true
}

func encodeArgon(salt, derived []byte) string {
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived))
}

func decodeArgon(s string) (salt, derived []byte, err error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, errors.New("not an argon2id hash")
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, err
	}
	derived, err = base64.RawStdEncoding.DecodeString(parts[5])
	return salt, derived, err
}

// --- HTTP middleware -------------------------------------------------------

// Resolver looks up an API-key principal from a prefix; the API uses this to
// avoid hashing on every request (cache the lookup with bounded TTL).
type Resolver func(ctx context.Context, prefix, display string) (*Principal, error)

// SessionCookie is the httpOnly cookie the OIDC callback sets, carrying the TEO
// JWT so a browser is authenticated on subsequent requests without a header.
const SessionCookie = "teo_session"

// Middleware returns an HTTP middleware that authenticates the request via
// either a JWT (Authorization: Bearer ..., or the teo_session cookie) or an API
// key (Authorization header with a `teo_*` prefix). Anonymous requests are
// passed through; downstream handlers enforce auth requirements.
func Middleware(jwtIssuer *JWTIssuer, resolveAPIKey Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var tok string
			if authz := r.Header.Get("Authorization"); authz != "" {
				tok = strings.TrimPrefix(authz, "Bearer ")
				tok = strings.TrimPrefix(tok, "bearer ")
				tok = strings.TrimSpace(tok)
			} else if c, err := r.Cookie(SessionCookie); err == nil {
				// Cookie tokens are always JWTs (never API keys), so they fall
				// through to the jwtIssuer.Verify branch below.
				tok = strings.TrimSpace(c.Value)
			}
			if tok == "" {
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			if strings.HasPrefix(tok, "teo_") {
				dot := strings.LastIndexByte(tok, '.')
				if dot > 0 && resolveAPIKey != nil {
					prefix := tok[:dot]
					p, err := resolveAPIKey(ctx, prefix, tok)
					if err == nil && p != nil {
						p.IsAPIKey = true
						ctx = WithPrincipal(ctx, p)
					}
				}
			} else if jwtIssuer != nil {
				if p, err := jwtIssuer.Verify(tok); err == nil {
					ctx = WithPrincipal(ctx, p)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Require returns a middleware that requires an authenticated principal with
// at least one of the given roles. Use after Middleware.
func Require(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFrom(r.Context())
			if p == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if len(roles) > 0 && !p.HasRole(roles...) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
