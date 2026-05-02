package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// IssuesClient calls the GitHub Issues REST API. It shares the same auth model
// as CheckClient (installation token).
type IssuesClient struct {
	HTTP      *http.Client
	BaseURL   string
	Token     string
	UserAgent string
}

// NewIssuesClient returns a configured Issues client.
func NewIssuesClient(token string) *IssuesClient {
	return &IssuesClient{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		BaseURL:   "https://api.github.com",
		Token:     token,
		UserAgent: "teo/issues",
	}
}

// IssueRequest is the body for POST /repos/{owner}/{repo}/issues.
type IssueRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	State     string   `json:"state,omitempty"` // "open" | "closed", set on PATCH
}

// Issue is the response shape we care about.
type Issue struct {
	Number    int    `json:"number"`
	HTMLURL   string `json:"html_url"`
	State     string `json:"state"`
	Title     string `json:"title"`
}

// Comment is a reply on an issue.
type Comment struct {
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

// Create POSTs a new issue and returns the GitHub-assigned number + URL.
func (c *IssuesClient) Create(ctx context.Context, repoFullName string, req IssueRequest) (Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/issues", c.BaseURL, repoFullName)
	var out Issue
	err := c.do(ctx, http.MethodPost, url, req, &out)
	return out, err
}

// Patch updates an existing issue (typically state=closed).
func (c *IssuesClient) Patch(ctx context.Context, repoFullName string, number int, req IssueRequest) (Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d", c.BaseURL, repoFullName, number)
	var out Issue
	err := c.do(ctx, http.MethodPatch, url, req, &out)
	return out, err
}

// Comment posts a comment to an existing issue.
func (c *IssuesClient) Comment(ctx context.Context, repoFullName string, number int, body string) (Comment, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", c.BaseURL, repoFullName, number)
	var out Comment
	err := c.do(ctx, http.MethodPost, url, map[string]string{"body": body}, &out)
	return out, err
}

// Get fetches an issue (used to read state for the SLA sweep).
func (c *IssuesClient) Get(ctx context.Context, repoFullName string, number int) (Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d", c.BaseURL, repoFullName, number)
	var out Issue
	err := c.do(ctx, http.MethodGet, url, nil, &out)
	return out, err
}

func (c *IssuesClient) do(ctx context.Context, method, url string, body, out any) error {
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("github %s %s: %d %s", method, url, resp.StatusCode, buf.String())
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
