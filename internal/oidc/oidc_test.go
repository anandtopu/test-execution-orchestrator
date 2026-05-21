package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// fakeIDP is an in-process OIDC provider: discovery + JWKS + token endpoints
// backed by a generated RSA key, so the whole verify path runs without network.
type fakeIDP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
	aud    string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: key, kid: "test-key", aud: "teo-client"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.issuer,
			"authorization_endpoint": f.issuer + "/auth",
			"token_endpoint":         f.issuer + "/token",
			"jwks_uri":               f.issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		pub := key.Public().(*rsa.PublicKey)
		eb := big.NewInt(int64(pub.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA", "kid": f.kid, "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(eb),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id_token":     f.signID(t, "alice", "alice@example.com", "nonce-xyz", time.Now().Add(time.Hour)),
			"access_token": "at",
			"token_type":   "Bearer",
		})
	})
	f.srv = httptest.NewServer(mux)
	f.issuer = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) signID(t *testing.T, sub, email, nonce string, exp time.Time) string {
	t.Helper()
	claims := idTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    f.issuer,
			Subject:   sub,
			Audience:  jwt.ClaimStrings{f.aud},
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:         email,
		EmailVerified: true,
		Nonce:         nonce,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.kid
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fakeIDP) provider(t *testing.T) *Provider {
	t.Helper()
	p, err := NewProvider(context.Background(), Config{
		IssuerURL: f.issuer, ClientID: f.aud, ClientSecret: "secret",
		RedirectURL: "https://teo.example/auth/callback",
	}, f.srv.Client())
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func TestDiscoveryAndAuthCodeURL(t *testing.T) {
	f := newFakeIDP(t)
	p := f.provider(t)

	u := p.AuthCodeURL("state-123", "nonce-xyz")
	for _, want := range []string{
		"response_type=code", "client_id=teo-client", "state=state-123",
		"nonce=nonce-xyz", "scope=openid+email+profile",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("auth URL missing %q\n  got %s", want, u)
		}
	}
	if !strings.HasPrefix(u, f.issuer+"/auth?") {
		t.Errorf("auth URL should target the discovered authorization_endpoint, got %s", u)
	}
}

func TestVerifyIDTokenHappyPath(t *testing.T) {
	f := newFakeIDP(t)
	p := f.provider(t)

	raw := f.signID(t, "alice", "alice@example.com", "nonce-xyz", time.Now().Add(time.Hour))
	claims, err := p.VerifyIDToken(context.Background(), raw, "nonce-xyz")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "alice" || claims.Email != "alice@example.com" || !claims.EmailVerified {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestVerifyIDTokenRejects(t *testing.T) {
	f := newFakeIDP(t)
	p := f.provider(t)
	ctx := context.Background()

	t.Run("nonce mismatch", func(t *testing.T) {
		raw := f.signID(t, "alice", "a@e.com", "real-nonce", time.Now().Add(time.Hour))
		if _, err := p.VerifyIDToken(ctx, raw, "expected-different"); err == nil {
			t.Fatal("want nonce-mismatch error")
		}
	})
	t.Run("expired", func(t *testing.T) {
		raw := f.signID(t, "alice", "a@e.com", "n", time.Now().Add(-time.Hour))
		if _, err := p.VerifyIDToken(ctx, raw, "n"); err == nil {
			t.Fatal("want expiry error")
		}
	})
	t.Run("wrong audience", func(t *testing.T) {
		claims := idTokenClaims{RegisteredClaims: jwt.RegisteredClaims{
			Issuer: f.issuer, Subject: "alice", Audience: jwt.ClaimStrings{"some-other-client"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = f.kid
		raw, _ := tok.SignedString(f.key)
		if _, err := p.VerifyIDToken(ctx, raw, ""); err == nil {
			t.Fatal("want audience error")
		}
	})
	t.Run("wrong signing key", func(t *testing.T) {
		other, _ := rsa.GenerateKey(rand.Reader, 2048)
		claims := idTokenClaims{RegisteredClaims: jwt.RegisteredClaims{
			Issuer: f.issuer, Subject: "alice", Audience: jwt.ClaimStrings{f.aud},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = f.kid
		raw, _ := tok.SignedString(other)
		if _, err := p.VerifyIDToken(ctx, raw, ""); err == nil {
			t.Fatal("want signature-verification error")
		}
	})
}

func TestExchange(t *testing.T) {
	f := newFakeIDP(t)
	p := f.provider(t)
	raw, err := p.Exchange(context.Background(), "auth-code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if _, err := p.VerifyIDToken(context.Background(), raw, "nonce-xyz"); err != nil {
		t.Fatalf("exchanged token failed verification: %v", err)
	}
}

func TestNewProviderRequiresConfig(t *testing.T) {
	if _, err := NewProvider(context.Background(), Config{}, nil); err == nil {
		t.Fatal("want error for empty config")
	}
}
