// Package config loads service configuration from env vars (TEO_*).
// Helm injects all values via env, so a config file is not required.
package config

import (
	"os"
	"time"
)

// Common holds settings shared by every service.
type Common struct {
	Env                 string // dev | staging | prod
	LogLevel            string // debug | info | warn | error
	OTLPEndpoint        string // for self-emitted spans (dogfood)
	HTTPListenAddr      string
	GRPCListenAddr      string
	PostgresDSN         string
	ClickHouseDSN       string
	NATSURL             string
	S3Endpoint          string
	S3Bucket            string
	S3Region            string
	JWTSecret           string
	JWTTTL              time.Duration
	GitHubWebhookSecret string // HMAC secret for inbound webhooks (FR-904)

	// ML predictor (E-12 / FR-607). When PredictorMLURL is set, the Run Manager
	// wraps the Heuristic in a Fallback that tries the Python LightGBM service at
	// this base URL first and falls back to the heuristic on any failure. When
	// unset, the heuristic is used directly (the v1.0 default). PredictorMLTimeout
	// bounds each ML call — and, because planning runs in-transaction, the time a
	// slow ML endpoint can hold a DB transaction open.
	PredictorMLURL     string
	PredictorMLTimeout time.Duration

	// OIDC sign-in (FR-801, S-03-02). When OIDCIssuer + OIDCClientID are set,
	// the API mounts the /auth/* sign-in routes; otherwise they return 503.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string // must match the IdP's registered redirect; default derived from UIBaseURL
	UIBaseURL        string // where a successful sign-in redirects the browser
}

// LoadCommon reads the common env vars.
func LoadCommon() Common {
	return Common{
		Env:                 getEnv("TEO_ENV", "dev"),
		LogLevel:            getEnv("TEO_LOG_LEVEL", "info"),
		OTLPEndpoint:        getEnv("TEO_OTLP_ENDPOINT", ""),
		HTTPListenAddr:      getEnv("TEO_HTTP_LISTEN", ":8080"),
		GRPCListenAddr:      getEnv("TEO_GRPC_LISTEN", ":9090"),
		PostgresDSN:         getEnv("TEO_POSTGRES_DSN", ""),
		ClickHouseDSN:       getEnv("TEO_CLICKHOUSE_DSN", ""),
		NATSURL:             getEnv("TEO_NATS_URL", "nats://localhost:4222"),
		S3Endpoint:          getEnv("TEO_S3_ENDPOINT", ""),
		S3Bucket:            getEnv("TEO_S3_BUCKET", "teo-artifacts"),
		S3Region:            getEnv("TEO_S3_REGION", "us-east-1"),
		JWTSecret:           getEnv("TEO_JWT_SECRET", ""),
		JWTTTL:              getDuration("TEO_JWT_TTL", time.Hour),
		GitHubWebhookSecret: getEnv("TEO_GITHUB_WEBHOOK_SECRET", ""),
		PredictorMLURL:      getEnv("TEO_PREDICTOR_ML_URL", ""),
		PredictorMLTimeout:  getDuration("TEO_PREDICTOR_ML_TIMEOUT", 3*time.Second),
		OIDCIssuer:          getEnv("TEO_OIDC_ISSUER", ""),
		OIDCClientID:        getEnv("TEO_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:    getEnv("TEO_OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:     getEnv("TEO_OIDC_REDIRECT_URL", ""),
		UIBaseURL:           getEnv("TEO_UI_BASE_URL", ""),
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
