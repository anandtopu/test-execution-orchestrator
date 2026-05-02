// Package config loads service configuration from env vars (TEO_*).
// Helm injects all values via env, so a config file is not required.
package config

import (
	"os"
	"time"
)

// Common holds settings shared by every service.
type Common struct {
	Env            string // dev | staging | prod
	LogLevel       string // debug | info | warn | error
	OTLPEndpoint   string // for self-emitted spans (dogfood)
	HTTPListenAddr string
	GRPCListenAddr string
	PostgresDSN    string
	ClickHouseDSN  string
	NATSURL        string
	S3Endpoint     string
	S3Bucket       string
	S3Region       string
	JWTSecret      string
	JWTTTL         time.Duration
}

// LoadCommon reads the common env vars.
func LoadCommon() Common {
	return Common{
		Env:            getEnv("TEO_ENV", "dev"),
		LogLevel:       getEnv("TEO_LOG_LEVEL", "info"),
		OTLPEndpoint:   getEnv("TEO_OTLP_ENDPOINT", ""),
		HTTPListenAddr: getEnv("TEO_HTTP_LISTEN", ":8080"),
		GRPCListenAddr: getEnv("TEO_GRPC_LISTEN", ":9090"),
		PostgresDSN:    getEnv("TEO_POSTGRES_DSN", ""),
		ClickHouseDSN:  getEnv("TEO_CLICKHOUSE_DSN", ""),
		NATSURL:        getEnv("TEO_NATS_URL", "nats://localhost:4222"),
		S3Endpoint:     getEnv("TEO_S3_ENDPOINT", ""),
		S3Bucket:       getEnv("TEO_S3_BUCKET", "teo-artifacts"),
		S3Region:       getEnv("TEO_S3_REGION", "us-east-1"),
		JWTSecret:      getEnv("TEO_JWT_SECRET", ""),
		JWTTTL:         getDuration("TEO_JWT_TTL", time.Hour),
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
