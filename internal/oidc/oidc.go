// Package oidc implements the slice of OpenID Connect that TEO needs to let a
// human sign in through an external IdP (Dex is preconfigured in the chart) and
// receive a short-lived TEO JWT in return (S-03-02 / FR-801, ADR-0014).
//
// It is deliberately dependency-free: discovery, the authorization-code
// exchange, and ID-token verification are built on net/http + crypto/rsa +
// golang-jwt (already vendored for the HS256 path). We implement only the
// Authorization Code flow with an RS256-signed ID token — the single mode Dex
// and every mainstream IdP support.
package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Config is the operator-supplied OIDC wiring. IssuerURL and ClientID are the
// minimum; ClientSecret is required for confidential clients (the default).
type Config struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string // defaults to ["openid","email","profile"]
}

// Provider is a discovered, ready-to-use OIDC relying-party client.
type Provider struct {
	cfg      Config
	issuer   string // the OP's canonical issuer (from discovery; used for `iss` validation)
	authURL  string
	tokenURL string
	hc       *http.Client
	keys     *keySet
}

// IDClaims is the subset of ID-token claims TEO consumes.
type IDClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
}

type discoveryDoc struct {
	Issuer        string `json:"issuer"`
	AuthEndpoint  string `json:"authorization_endpoint"`
	TokenEndpoint string `json:"token_endpoint"`
	JWKSURI       string `json:"jwks_uri"`
}

// NewProvider performs OIDC discovery against IssuerURL and returns a Provider.
// A nil httpClient gets a 10s-timeout default.
func NewProvider(ctx context.Context, cfg Config, httpClient *http.Client) (*Provider, error) {
	if cfg.IssuerURL == "" || cfg.ClientID == "" {
		return nil, errors.New("oidc: IssuerURL and ClientID are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}

	wellKnown := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	var doc discoveryDoc
	if err := getJSON(ctx, httpClient, wellKnown, &doc); err != nil {
		return nil, fmt.Errorf("oidc: discovery: %w", err)
	}
	if doc.AuthEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, errors.New("oidc: discovery document missing required endpoints")
	}
	issuer := doc.Issuer
	if issuer == "" {
		issuer = strings.TrimRight(cfg.IssuerURL, "/")
	}
	return &Provider{
		cfg:      cfg,
		issuer:   issuer,
		authURL:  doc.AuthEndpoint,
		tokenURL: doc.TokenEndpoint,
		hc:       httpClient,
		keys:     &keySet{uri: doc.JWKSURI, hc: httpClient, keys: map[string]*rsa.PublicKey{}},
	}, nil
}

// AuthCodeURL builds the redirect a browser follows to start sign-in. state and
// nonce are caller-generated CSRF/replay guards stored in short-lived cookies.
func (p *Provider) AuthCodeURL(state, nonce string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", p.cfg.ClientID)
	v.Set("redirect_uri", p.cfg.RedirectURL)
	v.Set("scope", strings.Join(p.cfg.Scopes, " "))
	v.Set("state", state)
	v.Set("nonce", nonce)
	return p.authURL + "?" + v.Encode()
}

type tokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// Exchange swaps an authorization code for the raw (still-unverified) ID token.
func (p *Provider) Exchange(ctx context.Context, code string) (rawIDToken string, err error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("client_id", p.cfg.ClientID)
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc: token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("oidc: decode token response (status %d): %w", resp.StatusCode, err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("oidc: token endpoint error: %s: %s", tr.Error, tr.ErrorDesc)
	}
	if resp.StatusCode != http.StatusOK || tr.IDToken == "" {
		return "", fmt.Errorf("oidc: token endpoint returned status %d with no id_token", resp.StatusCode)
	}
	return tr.IDToken, nil
}

type idTokenClaims struct {
	jwt.RegisteredClaims
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Nonce         string `json:"nonce"`
}

// VerifyIDToken validates the ID token's RS256 signature against the OP's JWKS
// and checks iss/aud/exp and the nonce. It returns the consumed claims.
func (p *Provider) VerifyIDToken(ctx context.Context, raw, expectedNonce string) (*IDClaims, error) {
	keyfunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return p.keys.key(ctx, kid)
	}
	tok, err := jwt.ParseWithClaims(raw, &idTokenClaims{}, keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(p.issuer),
		jwt.WithAudience(p.cfg.ClientID),
	)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify id token: %w", err)
	}
	c, ok := tok.Claims.(*idTokenClaims)
	if !ok || !tok.Valid {
		return nil, errors.New("oidc: invalid id token claims")
	}
	if expectedNonce != "" && c.Nonce != expectedNonce {
		return nil, errors.New("oidc: nonce mismatch")
	}
	if c.Subject == "" {
		return nil, errors.New("oidc: id token has no subject")
	}
	return &IDClaims{Subject: c.Subject, Email: c.Email, EmailVerified: c.EmailVerified}, nil
}

// --- JWKS -----------------------------------------------------------------

type keySet struct {
	uri  string
	hc   *http.Client
	mu   sync.Mutex
	keys map[string]*rsa.PublicKey
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// key returns the RSA public key for kid, refreshing the JWKS once on a miss
// (handles IdP key rotation without a restart).
func (k *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if pk, ok := k.keys[kid]; ok {
		return pk, nil
	}
	if err := k.refreshLocked(ctx); err != nil {
		return nil, err
	}
	if pk, ok := k.keys[kid]; ok {
		return pk, nil
	}
	// kid == "" can still match a single-key JWKS.
	if kid == "" && len(k.keys) == 1 {
		for _, pk := range k.keys {
			return pk, nil
		}
	}
	return nil, fmt.Errorf("oidc: no signing key for kid %q", kid)
}

func (k *keySet) refreshLocked(ctx context.Context) error {
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, k.hc, k.uri, &doc); err != nil {
		return fmt.Errorf("oidc: fetch jwks: %w", err)
	}
	next := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, key := range doc.Keys {
		if key.Kty != "RSA" {
			continue
		}
		pk, err := key.rsaPublicKey()
		if err != nil {
			continue // skip malformed keys rather than failing the whole set
		}
		next[key.Kid] = pk
	}
	if len(next) == 0 {
		return errors.New("oidc: jwks contained no usable RSA keys")
	}
	k.keys = next
	return nil
}

func (j jwk) rsaPublicKey() (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(j.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(j.E)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("oidc: zero RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}

func getJSON(ctx context.Context, hc *http.Client, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(dst)
}
