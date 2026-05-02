package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/audit"
	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/metrics"
)

// Config bundles API server configuration.
type Config struct {
	JWTSecret string
	JWTTTL    time.Duration
	JWTIssuer string
}

// Server wires the HTTP router for the TEO API.
type Server struct {
	cfg     Config
	pool    *pgxpool.Pool
	cache   *apiKeyCache
	jwt     *auth.JWTIssuer
	audit   *audit.Logger
	metrics *metrics.Registry
	spans   SpanQuerier
	mux     *chi.Mux
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
		runs := &RunsHandler{Pool: s.pool, Audit: s.audit}
		r.Route("/runs", func(r chi.Router) {
			runs.Routes(r)
			r.Get("/{id}/export", exportHandler(s.pool, s.spans))
		})
		r.Get("/quarantine/restore", quarantineRestoreHandler(s.pool))
	})

	// GraphQL read API for the Web UI (FR-701..706).
	r.Method("POST", "/graphql", graphqlHandler(s.pool))
	r.Method("GET", "/graphql/schema", schemaHandler())

	s.mux = r
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
}

type FailureCluster {
  id: ID!
  representativeMessage: String
  representativeStack: String
  occurrences: Int
  firstSeen: String
  lastSeen: String
}

type FlakeRecord {
  testId: ID!
  testPath: String
  testName: String
  flakeRate: Float
  wilsonLower: Float
  sampleSize: Int
  category: String
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

func (c *apiKeyCache) Get(prefix string) (*auth.Principal, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.store[prefix]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.principal, true
}

func (c *apiKeyCache) Put(prefix string, p *auth.Principal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[prefix] = apiKeyEntry{principal: p, expires: time.Now().Add(c.ttl)}
}

func (s *Server) resolveAPIKey(ctx context.Context, prefix, display string) (*auth.Principal, error) {
	if p, ok := s.cache.Get(prefix); ok {
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
	s.cache.Put(prefix, p)
	// fire-and-forget last_used_at update
	go func() {
		_, _ = s.pool.Exec(context.Background(),
			`UPDATE teo.api_keys SET last_used_at = now() WHERE id = $1`, id)
	}()
	return p, nil
}
