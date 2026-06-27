package llmhints

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/teo-dev/teo/internal/redact"
)

// Compile-time assertion that Client satisfies the Summarizer seam.
var _ Summarizer = (*Client)(nil)

const (
	defaultBaseURL     = "https://api.anthropic.com"
	defaultModel       = "claude-opus-4-8"
	anthropicVersion   = "2023-06-01"
	defaultMaxTokens   = 512
	defaultCallTimeout = 30 * time.Second
	maxMessageChars    = 2000
	maxStackChars      = 6000
)

// systemPrompt instructs the model to emit a single JSON object. We parse the
// response leniently (extractJSON) rather than constraining it with
// output_config.format — keeping the raw-HTTP path robust across model/account
// variations. Structured outputs are a documented future hardening (ADR-0021).
const systemPrompt = `You are a senior test-infrastructure engineer triaging a failing test cluster.
You are given a representative failure message and stack trace (already redacted of secrets) plus how often the cluster has occurred. Identify the most likely root cause.

Respond with ONLY a single JSON object, no markdown, no code fence, no prose:
{"category": "<one lowercase word>", "hint": "<1-3 sentences>", "confidence": <0.0-1.0>}

- "hint": name the most likely root cause and, when obvious, the fix direction. No preamble.
- "category": one of assertion, timeout, network, race, dependency, config, resource, oom, flaky, unknown.
- "confidence": your confidence in the hint.
If the signal is too weak to call, use category "unknown" with low confidence.`

// Client is a Summarizer backed by the Claude Messages API. It deliberately uses
// raw net/http rather than a generated SDK — mirroring internal/predictor's
// MLClient and the ADR-0019 precedent for a low-QPS, single-call-site external
// service (no extra dependency, no go.sum churn, builds offline). See ADR-0021.
//
// Client never panics or blocks indefinitely: each call is bounded by the HTTP
// client timeout, and Summarize is best-effort — a per-cluster failure logs and
// is omitted from the result rather than failing the batch.
type Client struct {
	APIKey  string
	Model   string // default claude-opus-4-8
	BaseURL string // default https://api.anthropic.com
	HTTP    *http.Client
	// Redactor scrubs secrets from Message/Stack before egress. Nil defaults to
	// redact.New() (the same default rule set the worker applies to logs).
	Redactor  *redact.Redactor
	Logger    *slog.Logger
	MaxTokens int
}

func (c *Client) model() string {
	if c.Model != "" {
		return c.Model
	}
	return defaultModel
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

func (c *Client) maxTokens() int {
	if c.MaxTokens > 0 {
		return c.MaxTokens
	}
	return defaultMaxTokens
}

func (c *Client) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Client) redactor() *redact.Redactor {
	if c.Redactor != nil {
		return c.Redactor
	}
	return redact.New()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultCallTimeout}
}

// Summarize generates a hint per cluster. A per-cluster error (transport,
// non-200, refusal, parse) is logged and the cluster is omitted from the result
// — the Runner records it as skipped. A nil/empty-key Client returns an error
// for the whole call (misconfiguration, not a per-cluster condition).
func (c *Client) Summarize(ctx context.Context, clusters []Cluster) ([]Hint, error) {
	if c == nil || c.APIKey == "" {
		return nil, fmt.Errorf("llmhints client not configured (missing API key)")
	}
	out := make([]Hint, 0, len(clusters))
	for _, cl := range clusters {
		h, err := c.summarizeOne(ctx, cl)
		if err != nil {
			c.logger().Warn("llm-hints summarize cluster failed", "cluster", cl.ID, "err", err)
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

func (c *Client) summarizeOne(ctx context.Context, cl Cluster) (Hint, error) {
	red := c.redactor()
	msg := truncate(red.String(cl.Message), maxMessageChars)
	stack := truncate(red.String(cl.Stack), maxStackChars)
	user := fmt.Sprintf("Failure message:\n%s\n\nStack trace:\n%s\n\nObserved occurrences: %d", msg, stack, cl.Occurrences)

	reqBody := anthropicRequest{
		Model:     c.model(),
		MaxTokens: c.maxTokens(),
		System:    systemPrompt,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return Hint{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return Hint{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return Hint{}, fmt.Errorf("transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Hint{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Hint{}, fmt.Errorf("decode response: %w", err)
	}
	// A 200 with stop_reason "refusal" carries no usable content — skip it.
	if out.StopReason == "refusal" {
		return Hint{}, fmt.Errorf("model refused to summarize cluster")
	}

	js := extractJSON(out.firstText())
	if js == "" {
		return Hint{}, fmt.Errorf("no JSON object in model response")
	}
	var parsed struct {
		Category   string  `json:"category"`
		Hint       string  `json:"hint"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(js), &parsed); err != nil {
		return Hint{}, fmt.Errorf("parse hint json: %w", err)
	}
	if parsed.Hint == "" {
		return Hint{}, fmt.Errorf("model returned empty hint")
	}
	return Hint{
		ClusterID:  cl.ID,
		Category:   parsed.Category,
		Hint:       parsed.Hint,
		Confidence: clampConfidence(parsed.Confidence),
	}, nil
}

// --- wire types -------------------------------------------------------------

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (r anthropicResponse) firstText() string {
	for _, b := range r.Content {
		if b.Type == "text" && b.Text != "" {
			return b.Text
		}
	}
	return ""
}

// --- helpers ----------------------------------------------------------------

// extractJSON returns the substring from the first '{' to the last '}', so a
// hint wrapped in stray prose or a code fence still parses. Returns "" when no
// brace pair is present.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated]"
}

func clampConfidence(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}
