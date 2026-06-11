package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssuesCreatePostsCorrectPayload(t *testing.T) {
	var got struct {
		method string
		path   string
		auth   string
		body   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		got.body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Issue{Number: 42, HTMLURL: "https://example.test/issues/42", State: "open"})
	}))
	defer srv.Close()

	c := NewIssuesClient("test-token")
	c.BaseURL = srv.URL
	out, err := c.Create(context.Background(), "owner/repo", IssueRequest{
		Title: "[TEO] Flaky test", Body: "details", Labels: []string{"teo", "flaky"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Number != 42 || out.HTMLURL == "" {
		t.Errorf("unexpected response: %+v", out)
	}
	if got.method != "POST" || got.path != "/repos/owner/repo/issues" {
		t.Errorf("bad request: %s %s", got.method, got.path)
	}
	if got.auth != "Bearer test-token" {
		t.Errorf("bad auth header: %q", got.auth)
	}
	for _, want := range []string{`"title":"[TEO] Flaky test"`, `"flaky"`, `"teo"`} {
		if !strings.Contains(got.body, want) {
			t.Errorf("body missing %q; got: %s", want, got.body)
		}
	}
}

func TestIssuesCreatePreservesMermaidFence(t *testing.T) {
	// Regression guard for the auto-quarantine run-history visualization
	// (gap [quarantine-mermaid]): a fenced ```mermaid xychart-beta block must
	// survive JSON-marshaling and reach the GitHub API byte-for-byte. If the
	// transport ever HTML-escaped or otherwise mangled the body, the fence would
	// break and GitHub would render raw text instead of a chart.
	const mermaid = "## Recent run history\n\n```mermaid\nxychart-beta\n    bar [1, 0, 1]\n```\n"

	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rawBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Issue{Number: 99, HTMLURL: "https://example.test/issues/99", State: "open"})
	}))
	defer srv.Close()

	c := NewIssuesClient("test-token")
	c.BaseURL = srv.URL
	if _, err := c.Create(context.Background(), "owner/repo", IssueRequest{
		Title: "[TEO] Flaky test quarantined", Body: mermaid,
	}); err != nil {
		t.Fatal(err)
	}

	// The literal fence-opening sequence must appear verbatim in the wire body.
	if !strings.Contains(rawBody, "```mermaid\\nxychart-beta") {
		t.Errorf("raw request body lost the mermaid fence; got: %s", rawBody)
	}
	// The bar series must survive too (no escaping of brackets/commas).
	if !strings.Contains(rawBody, "bar [1, 0, 1]") {
		t.Errorf("raw request body lost the bar series; got: %s", rawBody)
	}
	// Decoding the JSON must round-trip back to the exact mermaid block.
	var sent IssueRequest
	if err := json.Unmarshal([]byte(rawBody), &sent); err != nil {
		t.Fatalf("wire body is not valid JSON: %v", err)
	}
	if sent.Body != mermaid {
		t.Errorf("body mutated in transit:\nwant: %q\ngot:  %q", mermaid, sent.Body)
	}
}

func TestIssuesCommentSendsBody(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Comment{ID: 1, HTMLURL: "https://example.test/c/1"})
	}))
	defer srv.Close()
	c := NewIssuesClient("t")
	c.BaseURL = srv.URL
	if _, err := c.Comment(context.Background(), "owner/repo", 7, "ping"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receivedBody, `"body":"ping"`) {
		t.Errorf("body not sent: %s", receivedBody)
	}
}

func TestIssuesPatchClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		_ = json.NewEncoder(w).Encode(Issue{Number: 7, State: "closed"})
	}))
	defer srv.Close()
	c := NewIssuesClient("t")
	c.BaseURL = srv.URL
	out, err := c.Patch(context.Background(), "owner/repo", 7, IssueRequest{State: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "closed" {
		t.Errorf("state = %s, want closed", out.State)
	}
}

func TestIssuesErrorOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"validation failed"}`))
	}))
	defer srv.Close()
	c := NewIssuesClient("t")
	c.BaseURL = srv.URL
	_, err := c.Create(context.Background(), "owner/repo", IssueRequest{Title: "x"})
	if err == nil {
		t.Fatal("expected error on 422")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should include status code: %v", err)
	}
}
