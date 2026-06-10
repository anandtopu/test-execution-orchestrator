package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/logstore"
	"github.com/teo-dev/teo/internal/metrics"
	"github.com/teo-dev/teo/internal/runsvc"
)

// Config bundles API server configuration.
type Config struct {
	JWTSecret string
	JWTTTL    time.Duration
	JWTIssuer string
}

// Server wires the HTTP router for the TEO API.
type Server struct {
	cfg           Config
	pool          *pgxpool.Pool
	cache         *apiKeyCache
	jwt           *auth.JWTIssuer
	audit         *audit.Logger
	metrics       *metrics.Registry
	spans         SpanQuerier
	logPresigner  logstore.Presigner // nil when S3 isn't configured
	githubWebhook http.Handler       // nil when no secret is configured
	oidc          oidcAuthenticator  // nil when OIDC isn't configured
	uiBaseURL     string             // where the callback redirects the browser
	cookieSecure  bool               // set Secure on session cookies (https UIs)
	roleResolver  RoleResolver       // maps an OIDC identity to TEO roles
	runSvc        *runsvc.Service    // shared run-intake logic; nil → built from pool/audit
	mux           *chi.Mux
}

// New constructs a Server. If reg is nil a fresh metrics.Registry is created
// (every test gets its own — no global state).
func New(cfg Config, pool *pgxpool.Pool, opts ...Option) *Server {
	if cfg.JWTTTL == 0 {
		cfg.JWTTTL = time.Hour
	}
	if cfg.JWTIssuer == "" {
		cfg.JWTIssuer = "teo"
	}
	s := &Server{
		cfg:   cfg,
		pool:  pool,
		cache: newAPIKeyCache(30 * time.Second),
		jwt: &auth.JWTIssuer{
			Secret: []byte(cfg.JWTSecret),
			TTL:    cfg.JWTTTL,
			Issuer: cfg.JWTIssuer,
		},
		audit: &audit.Logger{Pool: pool},
	}
	for _, o := range opts {
		o(s)
	}
	if s.metrics == nil {
		s.metrics = metrics.New()
	}
	s.routes()
	return s
}

// Option is a functional option for New.
type Option func(*Server)

// WithRunService injects a shared run-intake service so the HTTP/GraphQL
// gateway and the gRPC Runs service operate over one instance. When absent, the
// RunsHandler builds an equivalent service from the server's pool and audit
// logger.
func WithRunService(svc *runsvc.Service) Option {
	return func(s *Server) {
		if svc != nil {
			s.runSvc = svc
		}
	}
}

// WithMetrics injects an existing metrics registry (so callers can share it
// across the API server and a separate /metrics listener).
func WithMetrics(r *metrics.Registry) Option {
	return func(s *Server) { s.metrics = r }
}

// WithSpanQuerier injects a SpanQuerier used by the OTLP run-export endpoint.
// When nil, /export?format=otlp returns 501.
func WithSpanQuerier(q SpanQuerier) Option {
	return func(s *Server) { s.spans = q }
}

// WithClickHouseConn is a convenience that wraps a chdriver.Conn as a
// SpanQuerier.
func WithClickHouseConn(conn chdriver.Conn) Option {
	return func(s *Server) {
		if conn != nil {
			s.spans = &chSpanQuerier{conn: conn}
		}
	}
}

// WithLogPresigner wires the per-test log endpoint
// (GET /api/v1/runs/{id}/tests/{execId}/log). When nil, that route returns 501.
func WithLogPresigner(p logstore.Presigner) Option {
	return func(s *Server) {
		if p != nil {
			s.logPresigner = p
		}
	}
}

// WithOIDC enables the sign-in routes (/auth/login, /auth/callback, ...). The
// uiBaseURL is where a successful callback redirects the browser; its scheme
// also decides whether session cookies are marked Secure. When this option is
// absent the /auth/* routes return 503.
func WithOIDC(provider oidcAuthenticator, uiBaseURL string) Option {
	return func(s *Server) {
		if provider != nil {
			s.oidc = provider
			s.uiBaseURL = uiBaseURL
			s.cookieSecure = strings.HasPrefix(uiBaseURL, "https")
		}
	}
}

// WithRoleResolver overrides how an authenticated OIDC identity is mapped to TEO
// roles (default: everyone gets RoleEngineer). cmd/api wires a DB-backed
// resolver that reads teo.user_roles.
func WithRoleResolver(r RoleResolver) Option {
	return func(s *Server) {
		if r != nil {
			s.roleResolver = r
		}
	}
}

// WithGitHubWebhook mounts an HMAC-verifying receiver at /webhooks/github.
// When the option isn't supplied, the route returns 404 — so an operator who
// hasn't configured TEO_GITHUB_WEBHOOK_SECRET can't accidentally accept
// unverified webhook traffic.
func WithGitHubWebhook(h http.Handler) Option {
	return func(s *Server) { s.githubWebhook = h }
}

// Metrics exposes the registry the server is using (for /metrics serving).
func (s *Server) Metrics() *metrics.Registry { return s.metrics }

// Handler returns the http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(metricsMiddleware(s.metrics))
	r.Use(auth.Middleware(s.jwt, s.resolveAPIKey))

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	r.Method("GET", "/metrics", s.metrics.Handler())

	r.Route("/api/v1", func(r chi.Router) {
		runs := &RunsHandler{Pool: s.pool, Audit: s.audit, Svc: s.runSvc}
		r.Route("/runs", func(r chi.Router) {
			runs.Routes(r)
			r.Get("/{id}/export", exportHandler(s.pool, s.spans))
			r.Get("/{id}/tests/{execId}/log", logURLHandler(s.pool, s.logPresigner))
		})
		r.Get("/quarantine/restore", quarantineRestoreHandler(s.pool))
	})

	// OIDC sign-in (FR-801, S-03-02). Always mounted; when OIDC isn't
	// configured the handlers return 503 so an operator can tell "not set up"
	// from "broken". /auth/session and /auth/refresh work off the JWT the
	// callback issued, so they don't depend on the provider being present.
	r.Route("/auth", func(r chi.Router) {
		r.Get("/login", s.oidcLogin)
		r.Get("/callback", s.oidcCallback)
		r.Post("/logout", s.oidcLogout)
		r.Get("/session", s.session)
		r.Post("/refresh", s.refresh)
	})

	// GraphQL read API for the Web UI (FR-701..706).
	r.Method("POST", "/graphql", graphqlHandler(s.pool))
	r.Method("GET", "/graphql/schema", schemaHandler())

	// GitHub App webhook receiver (FR-901..904). Always mounted; when no
	// handler is injected we return 503 so callers can distinguish "endpoint
	// gone" from "not configured."
	r.Method("POST", "/webhooks/github", githubWebhookRoute(s.githubWebhook))

	s.mux = r
}

// githubWebhookRoute returns h, or a stub that 503s when h is nil. Doing the
// nil check inside the handler keeps the route mount unconditional, which
// makes it easy to assert in tests that the path exists.
func githubWebhookRoute(h http.Handler) http.Handler {
	if h != nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusServiceUnavailable, "GitHub webhook not configured",
			"set TEO_GITHUB_WEBHOOK_SECRET to enable")
	})
}

// schemaHandler returns the SDL of the graphql schema. Useful for tooling
// and as a smoke endpoint.
func schemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(`type Query {
  runs(first: Int = 50): [Run!]!
  failureClusters: [FailureCluster!]!
  flakes: [FlakeRecord!]!
  costSummary(weeks: Int = 8): [CostWeek!]!
}

type CostWeek {
  weekStart: String!
  runs: Int!
  spotMinutes: Float!
  onDemandMinutes: Float!
  totalCost: Float!
  costPerBuild: Float!
  spotShare: Float!
}

type Run {
  id: ID!
  repoFullName: String
  branch: String
  commitSha: String
  status: String
  totalDurationMs: Int
  startedAt: String
  finishedAt: String
  preemptionCount: Int
  shards: [Shard!]
  failedTestCount: Int
  predictor: RunPredictor
  predictorMae: Float
  predictorRho: Float
  modelVersion: String
}

type RunPredictor {
  mae: Float
  rho: Float
  modelVersion: String
  p50DeltaMs: Int
  p95DeltaMs: Int
  sampleCount: Int
  confidence: Float
}

type Shard {
  id: ID!
  index: Int
  status: String
  workerId: String
  predictedDurationMs: Int
  actualDurationMs: Int
  testCount: Int
  startedAt: String
  finishedAt: String
  deltaMs: Int
  predictionConfidence: Float
  modelVersion: String
}

type FailureCluster {
  id: ID!
  representativeMessage: String
  representativeStack: String
  occurrences: Int
  firstSeen: String
  lastSeen: String
  x: Float
  y: Float
  r: Float
  category: String
  stackFingerprint: String
  affectedRuns: Int
}

type FlakeRecord {
  testId: ID!
  testPath: String
  testName: String
  flakeRate: Float
  wilsonLower: Float
  wilsonUpper: Float
  sampleSize: Int
  category: String
  spark: String
  status: String
  durationMeanMs: Int
  quarantinedAt: String
  ownerTeam: String
}
`))
	})
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

// --- API key lookup with bounded cache (NFR-SEC-805 — revocation in 30s) ---
//
// The cache is keyed on (prefix, sha256(secret)) — never on prefix alone.
// Keying on prefix would let an attacker who knows the prefix (it leaks in
// audit rows, error envelopes, server logs) authenticate with ANY secret
// during the 30s after a legitimate use, because the cache would hand back
// the principal without re-verifying the secret. SHA-256 is fine here as a
// cache key: the secret is already a cryptographically strong random value,
// so a fast hash gives the same uniqueness guarantee as argon2 without the
// ~100ms cost we'd otherwise pay on every cached request.

type apiKeyCache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	store map[string]apiKeyEntry
}

type apiKeyEntry struct {
	principal *auth.Principal
	expires   time.Time
}

func newAPIKeyCache(ttl time.Duration) *apiKeyCache {
	return &apiKeyCache{ttl: ttl, store: make(map[string]apiKeyEntry)}
}

// cacheKey binds the prefix to a hash of the full presented credential so a
// matching prefix with a wrong secret cannot hit a cache entry created by a
// legitimate request.
func cacheKey(prefix, display string) string {
	sum := sha256.Sum256([]byte(display))
	return prefix + "|" + hex.EncodeToString(sum[:])
}

func (c *apiKeyCache) Get(key string) (*auth.Principal, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.store[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.principal, true
}

func (c *apiKeyCache) Put(key string, p *auth.Principal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = apiKeyEntry{principal: p, expires: time.Now().Add(c.ttl)}
}

func (s *Server) resolveAPIKey(ctx context.Context, prefix, display string) (*auth.Principal, error) {
	ck := cacheKey(prefix, display)
	if p, ok := s.cache.Get(ck); ok {
		return p, nil
	}
	var (
		id     string
		hash   string
		scopes []string
	)
	err := s.pool.QueryRow(ctx, `
        SELECT id, hash, scopes FROM teo.api_keys
        WHERE prefix = $1 AND revoked_at IS NULL
          AND (expires_at IS NULL OR expires_at > now())
    `, prefix).Scan(&id, &hash, &scopes)
	if err != nil {
		return nil, err
	}
	if _, ok := auth.VerifyAPIKey(display, hash); !ok {
		return nil, http.ErrAbortHandler
	}
	p := &auth.Principal{
		APIKeyID: id,
		Roles:    []auth.Role{auth.RoleEngineer},
		Scopes:   scopes,
		IsAPIKey: true,
	}
	s.cache.Put(ck, p)
	// fire-and-forget last_used_at update
	go func() {
		_, _ = s.pool.Exec(context.Background(),
			`UPDATE teo.api_keys SET last_used_at = now() WHERE id = $1`, id)
	}()
	return p, nil
}
