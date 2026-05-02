package quarantine

import (
	"context"
	"errors"

	"github.com/teo-dev/teo/internal/github"
)

// GitHubOpener implements IssueOpener against the GitHub Issues REST API.
// One Open call results in one POST /repos/{owner}/{repo}/issues.
type GitHubOpener struct {
	Client *github.IssuesClient
}

// Open implements IssueOpener.
func (g *GitHubOpener) Open(ctx context.Context, repoFullName, title, body string, assignees, labels []string) (number int, url string, err error) {
	if g == nil || g.Client == nil {
		return 0, "", errors.New("github issues client not configured")
	}
	issue, err := g.Client.Create(ctx, repoFullName, github.IssueRequest{
		Title:     title,
		Body:      body,
		Assignees: assignees,
		Labels:    labels,
	})
	if err != nil {
		return 0, "", err
	}
	return issue.Number, issue.HTMLURL, nil
}

// Comment implements IssueCommenter.
func (g *GitHubOpener) Comment(ctx context.Context, repoFullName string, number int, body string) error {
	if g == nil || g.Client == nil {
		return errors.New("github issues client not configured")
	}
	_, err := g.Client.Comment(ctx, repoFullName, number, body)
	return err
}
