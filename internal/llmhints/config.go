package llmhints

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/teo-dev/teo/internal/redact"
)

// Enabled reports whether the LLM-hints feature is switched on. It is opt-in:
// the operator must set TEO_LLM_HINTS_ENABLED to a truthy value (1/true/yes/on).
// ADR-0021 makes it default-off because the feature sends (redacted) failure
// data to an external LLM API.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TEO_LLM_HINTS_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// NewClientFromEnv builds a Client from environment configuration, or returns
// (nil, false) when ANTHROPIC_API_KEY is unset. Reads:
//
//	ANTHROPIC_API_KEY         required (returns false if empty)
//	TEO_LLM_HINTS_MODEL       default claude-opus-4-8
//	TEO_LLM_HINTS_MAX_TOKENS  default 512
//	ANTHROPIC_BASE_URL        optional override (e.g. a gateway)
func NewClientFromEnv(logger *slog.Logger) (*Client, bool) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, false
	}
	maxTokens := defaultMaxTokens
	if v := os.Getenv("TEO_LLM_HINTS_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTokens = n
		}
	}
	c := &Client{
		APIKey:    key,
		Model:     getenvDefault("TEO_LLM_HINTS_MODEL", defaultModel),
		BaseURL:   os.Getenv("ANTHROPIC_BASE_URL"),
		HTTP:      &http.Client{Timeout: defaultCallTimeout},
		Redactor:  redact.New(),
		Logger:    logger,
		MaxTokens: maxTokens,
	}
	return c, true
}

func getenvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
