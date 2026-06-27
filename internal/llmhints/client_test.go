package llmhints

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// awsKey is a syntactically-valid AWS access key (AKIA + 16 chars) so the
// default redactor's aws_access_key rule fires.
const awsKey = "AKIAIOSFODNN7EXAMPLE"

func okResponse(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":` + jsonString(text) + `}]}`))
	}
}

// jsonString quotes s as a JSON string literal (so canned text can contain
// quotes/braces without hand-escaping in every test).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestClientRedactsSecretsBeforeEgress(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		require.Equal(t, "test-key", r.Header.Get("x-api-key"))
		require.Equal(t, anthropicVersion, r.Header.Get("anthropic-version"))
		okResponse(`{"category":"assertion","hint":"expected 200 got 500","confidence":0.8}`)(w, r)
	}))
	defer srv.Close()

	c := &Client{APIKey: "test-key", BaseURL: srv.URL, HTTP: srv.Client()}
	hints, err := c.Summarize(context.Background(), []Cluster{{
		ID:          "c1",
		Message:     awsKey + " leaked in failure message",
		Stack:       "at handler (" + awsKey + ")",
		Occurrences: 3,
	}})
	require.NoError(t, err)
	require.Len(t, hints, 1)
	require.Equal(t, "c1", hints[0].ClusterID)
	require.Equal(t, "assertion", hints[0].Category)
	require.Equal(t, "expected 200 got 500", hints[0].Hint)
	require.InDelta(t, 0.8, hints[0].Confidence, 0.001)

	// The load-bearing assertion: the secret never went over the wire, and the
	// redaction marker did.
	require.NotContains(t, string(gotBody), awsKey)
	require.Contains(t, string(gotBody), "[REDACTED:aws_access_key]")
}

func TestClientParsesJSONWrappedInProse(t *testing.T) {
	srv := httptest.NewServer(okResponse("Here is the analysis:\n```json\n{\"category\":\"timeout\",\"hint\":\"DB call exceeded 30s\",\"confidence\":0.6}\n```"))
	defer srv.Close()

	c := &Client{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	hints, err := c.Summarize(context.Background(), []Cluster{{ID: "c1", Message: "m", Stack: "s"}})
	require.NoError(t, err)
	require.Len(t, hints, 1)
	require.Equal(t, "timeout", hints[0].Category)
	require.Equal(t, "DB call exceeded 30s", hints[0].Hint)
}

func TestClientClampsConfidence(t *testing.T) {
	srv := httptest.NewServer(okResponse(`{"category":"flaky","hint":"nondeterministic ordering","confidence":1.5}`))
	defer srv.Close()

	c := &Client{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	hints, err := c.Summarize(context.Background(), []Cluster{{ID: "c1"}})
	require.NoError(t, err)
	require.Len(t, hints, 1)
	require.Equal(t, 1.0, hints[0].Confidence)
}

func TestClientRefusalIsSkippedNotFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"stop_reason":"refusal","content":[]}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	hints, err := c.Summarize(context.Background(), []Cluster{{ID: "c1"}})
	require.NoError(t, err)
	require.Empty(t, hints, "a refusal yields no hint, not an error")
}

func TestClientNon200IsSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	hints, err := c.Summarize(context.Background(), []Cluster{{ID: "c1"}})
	require.NoError(t, err)
	require.Empty(t, hints)
}

func TestClientNotConfiguredErrors(t *testing.T) {
	c := &Client{} // no API key
	_, err := c.Summarize(context.Background(), []Cluster{{ID: "c1"}})
	require.Error(t, err)
}

func TestExtractJSON(t *testing.T) {
	require.Equal(t, `{"a":1}`, extractJSON(`prefix {"a":1} suffix`))
	require.Equal(t, `{"a":{"b":2}}`, extractJSON("```\n{\"a\":{\"b\":2}}\n```"))
	require.Equal(t, "", extractJSON("no object here"))
	require.Equal(t, "", extractJSON("}{"))
}

func TestTruncate(t *testing.T) {
	require.Equal(t, "abc", truncate("abc", 5))
	require.Equal(t, "ab\n…[truncated]", truncate("abcd", 2))
}
