package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CheckClient creates and updates Check Runs via the GitHub REST API.
// Auth uses an installation token (acquired via JWT signed with the App private key);
// for the MVP this is the operator-provided token.
type CheckClient struct {
	HTTP      *http.Client
	BaseURL   string // e.g., https://api.github.com
	Token     string // installation token
	UserAgent string
}

// NewCheckClient returns a configured client.
func NewCheckClient(token string) *CheckClient {
	return &CheckClient{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		BaseURL:   "https://api.github.com",
		Token:     token,
		UserAgent: "teo/checks",
	}
}

// CheckRun is what we POST/PATCH.
type CheckRun struct {
	Name        string     `json:"name"`
	HeadSHA     string     `json:"head_sha"`
	Status      string     `json:"status,omitempty"`     // queued | in_progress | completed
	Conclusion  string     `json:"conclusion,omitempty"` // success | failure | neutral | canceled | skipped
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DetailsURL  string     `json:"details_url,omitempty"`
	Output      *Output    `json:"output,omitempty"`
}

// Output is the rich Check Run payload.
type Output struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Text    string `json:"text,omitempty"`
}

// Create POSTs a new check run; returns the GitHub-assigned ID.
func (c *CheckClient) Create(ctx context.Context, repoFullName string, run CheckRun) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/check-runs", c.BaseURL, repoFullName)
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, url, run, &resp); err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// Update PATCHes an existing check run.
func (c *CheckClient) Update(ctx context.Context, repoFullName string, id int64, run CheckRun) error {
	url := fmt.Sprintf("%s/repos/%s/check-runs/%d", c.BaseURL, repoFullName, id)
	return c.do(ctx, http.MethodPatch, url, run, nil)
}

func (c *CheckClient) do(ctx context.Context, method, url string, body, out any) error {
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
