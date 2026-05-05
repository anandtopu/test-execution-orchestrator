package quarantine

import (
	"context"
	"strings"
	"testing"
)

// The success paths require a real GitHub HTTP client and are covered in
// internal/github/issues_test.go. Here we pin the nil-receiver and
// nil-client guards that prevent a misconfigured Daemon from panicking.

func TestGitHubOpenerNilReceiverOpenReturnsError(t *testing.T) {
	var g *GitHubOpener
	number, url, err := g.Open(context.Background(), "owner/repo", "t", "b", nil, nil)
	if err == nil {
		t.Fatal("expected error from nil receiver")
	}
	if number != 0 || url != "" {
		t.Fatalf("expected zero-values, got number=%d url=%s", number, url)
	}
	if !strings.Contains(err.Error(), "github issues client") {
		t.Errorf("error message lost the configuration hint: %v", err)
	}
}

func TestGitHubOpenerNilClientOpenReturnsError(t *testing.T) {
	g := &GitHubOpener{Client: nil}
	if _, _, err := g.Open(context.Background(), "owner/repo", "t", "b", nil, nil); err == nil {
		t.Fatal("expected error when Client is nil")
	}
}

func TestGitHubOpenerNilReceiverCommentReturnsError(t *testing.T) {
	var g *GitHubOpener
	if err := g.Comment(context.Background(), "owner/repo", 1, "body"); err == nil {
		t.Fatal("expected error from nil receiver")
	}
}

func TestGitHubOpenerNilClientCommentReturnsError(t *testing.T) {
	g := &GitHubOpener{Client: nil}
	if err := g.Comment(context.Background(), "owner/repo", 1, "body"); err == nil {
		t.Fatal("expected error when Client is nil")
	}
}
