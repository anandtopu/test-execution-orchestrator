package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/model"
	"github.com/teo-dev/teo/internal/runmanager"
)

// CheckObserver implements runmanager.RunObserver. It maintains a GitHub
// Check Run in lockstep with each TEO run.
type CheckObserver struct {
	Pool    *pgxpool.Pool
	Logger  *slog.Logger
	Client  *CheckClient
	BaseURL string // for deep links: "https://teo.example.com"
	AppName string // shown as the Check Run name; default "TEO"
}

// OnRunStateChanged implements runmanager.RunObserver.
func (o *CheckObserver) OnRunStateChanged(ctx context.Context, snap runmanager.RunSnapshot, _ model.RunStatus) error {
	if o == nil || o.Client == nil {
		return nil
	}
	name := o.AppName
	if name == "" {
		name = "TEO"
	}
	deepURL := fmt.Sprintf("%s/runs/%s", strings.TrimRight(o.BaseURL, "/"), snap.ID)

	// Create the Check Run on the first transition out of pending. We do this
	// only once; subsequent transitions update the existing Check Run.
	if snap.GitHubCheckRunID == nil {
		now := time.Now()
		req := CheckRun{
			Name:       name,
			HeadSHA:    snap.CommitSHA,
			Status:     "in_progress",
			StartedAt:  &now,
			DetailsURL: deepURL,
			Output: &Output{
				Title:   "TEO is running this commit's tests",
				Summary: o.summary(ctx, snap),
			},
		}
		id, err := o.Client.Create(ctx, snap.RepoFullName, req)
		if err != nil {
			return fmt.Errorf("create check run: %w", err)
		}
		_, err = o.Pool.Exec(ctx,
			`UPDATE teo.runs SET github_check_run_id = $1 WHERE id = $2`,
			id, snap.ID)
		if err != nil {
			return fmt.Errorf("persist check run id: %w", err)
		}
		return nil
	}

	// Run already has a Check Run; either update progress or finalize.
	if isTerminal(snap.Status) {
		return o.finalize(ctx, snap, deepURL, name)
	}
	// In-flight progress update — best-effort.
	now := time.Now()
	return o.Client.Update(ctx, snap.RepoFullName, *snap.GitHubCheckRunID, CheckRun{
		Name:       name,
		HeadSHA:    snap.CommitSHA,
		Status:     "in_progress",
		StartedAt:  snap.StartedAt,
		DetailsURL: deepURL,
		Output: &Output{
			Title:   fmt.Sprintf("TEO: %s", snap.Status),
			Summary: o.summary(ctx, snap),
		},
		CompletedAt: &now, // ignored by GitHub when status != completed; harmless to send
	})
}

func (o *CheckObserver) finalize(ctx context.Context, snap runmanager.RunSnapshot, deepURL, name string) error {
	conclusion := "success"
	title := "TEO: all tests passed"
	switch snap.Status {
	case model.RunFailed:
		conclusion = "failure"
		title = "TEO: tests failed"
	case model.RunCancelled:
		conclusion = "canceled"
		title = "TEO: run canceled"
	}
	body := o.finalSummary(ctx, snap)
	return o.Client.Update(ctx, snap.RepoFullName, *snap.GitHubCheckRunID, CheckRun{
		Name:        name,
		HeadSHA:     snap.CommitSHA,
		Status:      "completed",
		Conclusion:  conclusion,
		StartedAt:   snap.StartedAt,
		CompletedAt: snap.FinishedAt,
		DetailsURL:  deepURL,
		Output: &Output{
			Title:   title,
			Summary: body.Summary,
			Text:    body.Text,
		},
	})
}

// shardCounts is a small DTO for summary-line formatting.
type shardCounts struct {
	Total, Pending, Running, Done, Failed, Lost int
}

// outputBody is what the finalize step writes — Markdown chunks for the Check
// Run "summary" (short) and "text" (long).
type outputBody struct {
	Summary string
	Text    string
}

// summary renders the in-flight Markdown for the Check Run. Includes shard
// progress and a deep link.
func (o *CheckObserver) summary(ctx context.Context, snap runmanager.RunSnapshot) string {
	c := o.shardCounts(ctx, snap.ID)
	return fmt.Sprintf(
		"Status: **%s**\n\nShards: %d/%d done · %d running · %d pending · %d failed",
		snap.Status, c.Done, c.Total, c.Running, c.Pending, c.Failed,
	)
}

// finalSummary renders the terminal Markdown — short summary + a longer text
// block that includes the top-3 failure clusters (S-10-03).
func (o *CheckObserver) finalSummary(ctx context.Context, snap runmanager.RunSnapshot) outputBody {
	c := o.shardCounts(ctx, snap.ID)
	dur := "—"
	if snap.TotalDurationMS > 0 {
		dur = fmt.Sprintf("%ds", snap.TotalDurationMS/1000)
	}
	short := fmt.Sprintf(
		"Outcome: **%s** · %d/%d shards passed · %d failed · %s wall-clock",
		conclusionLabel(snap.Status), c.Done-c.Failed-c.Lost, c.Total, c.Failed, dur,
	)
	if snap.PreemptionCount > 0 {
		short += fmt.Sprintf("\n\n_%d spot-preemption(s) handled during this run._", snap.PreemptionCount)
	}
	clusters := o.topClusters(ctx, snap.ID, 3)
	long := buildClusterMarkdown(clusters)
	return outputBody{Summary: short, Text: long}
}

func conclusionLabel(s model.RunStatus) string {
	switch s {
	case model.RunSucceeded:
		return "passed"
	case model.RunFailed:
		return "failed"
	case model.RunCancelled:
		return "canceled"
	}
	return string(s)
}

// shardCounts queries shard status counts for a run.
func (o *CheckObserver) shardCounts(ctx context.Context, runID string) shardCounts {
	var c shardCounts
	rows, err := o.Pool.Query(ctx, `
        SELECT status, count(*) FROM teo.shards WHERE run_id = $1 GROUP BY status
    `, runID)
	if err != nil {
		return c
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			continue
		}
		c.Total += n
		switch st {
		case "pending":
			c.Pending = n
		case "running":
			c.Running = n
		case "succeeded":
			c.Done += n
		case "failed":
			c.Done += n
			c.Failed += n
		case "lost", "preempted":
			c.Lost += n
			c.Done += n
		}
	}
	return c
}

// ClusterSummary is one row of the top-N failure-cluster table.
type ClusterSummary struct {
	Message     string
	Stack       string
	Occurrences int64
}

// topClusters returns the top-N failure clusters touched by this run, ranked
// by occurrence count then recency.
func (o *CheckObserver) topClusters(ctx context.Context, runID string, n int) []ClusterSummary {
	rows, err := o.Pool.Query(ctx, `
        SELECT fc.representative_message,
               fc.representative_stack,
               count(*) AS occurrences_in_run
        FROM teo.test_executions te
        JOIN teo.shards s ON s.id = te.shard_id
        JOIN teo.failure_clusters fc ON fc.id = te.failure_cluster_id
        WHERE s.run_id = $1
        GROUP BY fc.id, fc.representative_message, fc.representative_stack
        ORDER BY occurrences_in_run DESC, fc.last_seen DESC
        LIMIT $2
    `, runID, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ClusterSummary
	for rows.Next() {
		var c ClusterSummary
		if err := rows.Scan(&c.Message, &c.Stack, &c.Occurrences); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// buildClusterMarkdown renders the top-N failure clusters as a Markdown
// document for the Check Run text body.
func buildClusterMarkdown(clusters []ClusterSummary) string {
	if len(clusters) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Top failure clusters\n\n")
	for i, c := range clusters {
		fmt.Fprintf(&b, "**#%d** — %d occurrences\n\n", i+1, c.Occurrences)
		msg := c.Message
		if msg == "" {
			msg = "(no message)"
		}
		fmt.Fprintf(&b, "> %s\n\n", msg)
		stack := c.Stack
		if len(stack) > 1024 {
			stack = stack[:1024] + "\n…(truncated)"
		}
		b.WriteString("```\n")
		b.WriteString(stack)
		if !strings.HasSuffix(stack, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	return b.String()
}

// isTerminal mirrors runmanager.IsTerminal but lives in the github package to
// avoid an unnecessary import. Kept in sync with the state machine.
func isTerminal(s model.RunStatus) bool {
	return s == model.RunSucceeded || s == model.RunFailed || s == model.RunCancelled
}
