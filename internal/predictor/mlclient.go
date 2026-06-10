package predictor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/teo-dev/teo/internal/model"
)

// Compile-time assertion that MLClient satisfies the Predictor interface.
var _ Predictor = (*MLClient)(nil)

// MLClient is an HTTP client for the Python LightGBM predictor service
// (services/predictor-ml). It POSTs to <BaseURL>/v1/predict and maps the JSON
// response back onto []Prediction.
//
// NOTE: ADR-0019 specifies a gRPC contract for the predictor, but the Python
// service only implements the FastAPI HTTP surface today and the v1.0 wiring
// targets that HTTP endpoint. This is a deliberate, documented divergence from
// the ADR; see progress.md E-12 / FR-607.
//
// MLClient never panics or blocks indefinitely: it bounds every call by Timeout
// and returns an error on any failure. The Fallback wrapper turns those errors
// into a transparent heuristic fallback so a down/slow ML service never breaks
// scheduling.
type MLClient struct {
	BaseURL string
	HTTP    *http.Client
	// Timeout bounds each predict call. Because planning happens inside the Run
	// Manager's DB transaction (manager.go plan()), this directly bounds how long
	// a slow ML endpoint can hold that transaction open. Keep it short.
	Timeout time.Duration
	// Logger logs server-side cold-start degradation. May be nil (defaults to
	// slog.Default() at use).
	Logger *slog.Logger
	// OnServerColdStart is invoked once per Predict call when the ML server
	// itself returned used_fallback=true (a successful 200 in which the server
	// silently degraded to cold-start, e.g. MAE drift / model not loaded). This
	// is distinct from a client-side Fallback (timeout/non-200/decode/length
	// mismatch), which the Fallback wrapper counts via its own OnFallback. Wire
	// this to a teo_predictor_server_coldstart_total counter so the MAE-drift
	// condition is observable. May be nil.
	OnServerColdStart func()
}

// NewMLClient builds an MLClient against the given base URL with a sane timeout.
func NewMLClient(baseURL string, timeout time.Duration) *MLClient {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &MLClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: timeout},
		Timeout: timeout,
	}
}

// mlTestEntry mirrors the Python models.TestEntry schema (snake_case JSON).
type mlTestEntry struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	ParamsHash string   `json:"params_hash,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

// mlPredictRequest mirrors the Python models.PredictRequest schema.
type mlPredictRequest struct {
	RepoFullName string        `json:"repo_full_name"`
	Tests        []mlTestEntry `json:"tests"`
}

// mlPrediction mirrors the Python models.Prediction schema. Field names MUST
// match the Python snake_case exactly or durations silently decode to zero.
type mlPrediction struct {
	Fingerprint      string  `json:"fingerprint"`
	P50DurationMS    int     `json:"p50_duration_ms"`
	P95DurationMS    int     `json:"p95_duration_ms"`
	FlakeProbability float32 `json:"flake_probability"`
	IsColdStart      bool    `json:"is_cold_start"`
	ModelVersion     string  `json:"model_version"`
	Confidence       float64 `json:"confidence"`
}

// mlPredictResponse mirrors the Python models.PredictResponse schema.
type mlPredictResponse struct {
	Predictions      []mlPrediction `json:"predictions"`
	UsedFallback     bool           `json:"used_fallback"`
	UsedModelVersion string         `json:"used_model_version"`
}

// Predict POSTs the test manifest to the ML service and returns one Prediction
// per input test, in order. An empty input returns (nil, nil) WITHOUT issuing an
// HTTP request.
func (c *MLClient) Predict(ctx context.Context, repoFullName string, tests []model.TestEntry) ([]Prediction, error) {
	if c == nil || c.BaseURL == "" {
		return nil, fmt.Errorf("ml client not configured")
	}
	if len(tests) == 0 {
		return nil, nil
	}

	reqBody := mlPredictRequest{
		RepoFullName: repoFullName,
		Tests:        make([]mlTestEntry, len(tests)),
	}
	for i, t := range tests {
		reqBody.Tests[i] = mlTestEntry{
			Path:       t.Path,
			Name:       t.Name,
			ParamsHash: t.ParamsHash,
			Tags:       t.Tags,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ml predict: marshal request: %w", err)
	}

	// Bound the call by Timeout even if the caller's ctx has none.
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/predict", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ml predict: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: c.Timeout}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ml predict: transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ml predict: unexpected status %d", resp.StatusCode)
	}

	var out mlPredictResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ml predict: decode response: %w", err)
	}
	if len(out.Predictions) != len(tests) {
		return nil, fmt.Errorf("ml predict: length mismatch: got %d predictions for %d tests",
			len(out.Predictions), len(tests))
	}

	// The server returned a usable 200 but flagged that IT degraded to cold-start
	// (model not loaded / MAE drift). Surface that so the degradation is
	// observable — it is invisible to the Fallback wrapper, which only sees a
	// successful response.
	if out.UsedFallback {
		logger := c.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("ml predictor server used cold-start fallback",
			"repo", repoFullName,
			"tests", len(tests),
			"model_version", out.UsedModelVersion,
		)
		if c.OnServerColdStart != nil {
			c.OnServerColdStart()
		}
	}

	preds := make([]Prediction, len(out.Predictions))
	for i, p := range out.Predictions {
		preds[i] = Prediction{
			Fingerprint:      p.Fingerprint,
			P50DurationMS:    p.P50DurationMS,
			P95DurationMS:    p.P95DurationMS,
			FlakeProbability: p.FlakeProbability,
			IsColdStart:      p.IsColdStart,
		}
	}
	return preds, nil
}
