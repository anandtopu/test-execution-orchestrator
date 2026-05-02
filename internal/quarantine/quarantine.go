// Package quarantine implements the auto-quarantine workflow (FR-605, FR-609).
// On a Wilson-confirmed flake, the daemon transitions tests.status to
// 'quarantined', resolves CODEOWNERS, and opens a GitHub Issue.
package quarantine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/github"
)

// Daemon scans for newly-confirmed flakes and quarantines them.
type Daemon struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	GHClient     *github.CheckClient
	IssueOpener  IssueOpener
	IssueCommenter IssueCommenter
}

// IssueOpener creates a GitHub Issue for a quarantined test.
type IssueOpener interface {
	Open(ctx context.Context, repoFullName string, title, body string, assignees, labels []string) (number int, url string, err error)
}

// IssueCommenter posts a comment to an existing issue. Used to dedupe when a
// test re-quarantines after un-quarantine, and by the SLA / un-quarantine
// proposal sweeps.
type IssueCommenter interface {
	Comment(ctx context.Context, repoFullName string, number int, body string) error
}

// Run does one sweep over flake_records and tests, quarantining new flakes.
func (d *Daemon) Run(ctx context.Context) error {
	rows, err := d.Pool.Query(ctx, `
        SELECT t.id, t.repo_id, t.path, t.name, repos.full_name, repos.auto_quarantine_enabled,
               fr.flake_rate, fr.wilson_lower, fr.sample_size
        FROM teo.flake_records fr
        JOIN teo.tests t ON t.id = fr.test_id
        JOIN teo.repos repos ON repos.id = t.repo_id
        WHERE fr.wilson_lower > 0.05
          AND fr.sample_size >= 20
          AND t.status = 'active'
          AND fr.quarantined_at IS NULL
    `)
	if err != nil {
		return err
	}
	defer rows.Close()

	type cand struct {
		testID, repoID, path, name, repoFull string
		autoEnabled                          bool
		rate                                 float64
		lower                                float64
		samples                              int
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.testID, &c.repoID, &c.path, &c.name, &c.repoFull, &c.autoEnabled,
			&c.rate, &c.lower, &c.samples); err != nil {
			return err
		}
		cands = append(cands, c)
	}

	for _, c := range cands {
		if !c.autoEnabled {
			d.Logger.Info("auto-quarantine disabled, skipping", "test", c.testID, "repo", c.repoFull)
			continue
		}
		if err := d.quarantine(ctx, c.testID, c.repoFull, c.path, c.name, c.rate, c.samples); err != nil {
			d.Logger.Error("quarantine failed", "test", c.testID, "err", err)
		}
	}
	return nil
}

func (d *Daemon) quarantine(ctx context.Context, testID, repoFull, path, name string, rate float64, samples int) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
        UPDATE teo.tests
        SET status = 'quarantined',
            quarantined_at = now(),
            quarantine_reason = 'auto: wilson-confirmed flaky'
        WHERE id = $1 AND status = 'active'
    `, testID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
        UPDATE teo.flake_records
        SET quarantined_at = now()
        WHERE test_id = $1
    `, testID); err != nil {
		return err
	}

	// If a prior issue exists for this test, dedupe by commenting rather than
	// opening a duplicate (S-15-02 AC4).
	var existingNumber int
	_ = d.Pool.QueryRow(ctx, `
        SELECT COALESCE(github_issue_number, 0) FROM teo.flake_records WHERE test_id = $1
    `, testID).Scan(&existingNumber)

	if existingNumber > 0 && d.IssueCommenter != nil {
		body := fmt.Sprintf("Re-quarantined after re-detection. Failure rate %.1f%% over %d attempts in the last 30 days.", rate*100, samples)
		if err := d.IssueCommenter.Comment(ctx, repoFull, existingNumber, body); err != nil {
			d.Logger.Warn("issue comment failed", "issue", existingNumber, "err", err)
		}
		return tx.Commit(ctx)
	}

	number, url := 0, ""
	if d.IssueOpener != nil {
		title := fmt.Sprintf("[TEO] Flaky test quarantined: %s", name)
		body := buildIssueBody(path, name, rate, samples)
		number, url, err = d.IssueOpener.Open(ctx, repoFull, title, body, nil, []string{"teo", "flaky", "auto-generated"})
		if err != nil {
			d.Logger.Warn("issue create failed", "err", err)
		}
	}
	if number > 0 {
		_, _ = tx.Exec(ctx, `
            UPDATE teo.flake_records
            SET github_issue_number = $1, github_issue_url = $2
            WHERE test_id = $3
        `, number, url, testID)
	}
	return tx.Commit(ctx)
}

func buildIssueBody(path, name string, rate float64, samples int) string {
	var b strings.Builder
	b.WriteString("## Flaky test detected\n\n")
	fmt.Fprintf(&b, "- **Path:** `%s`\n", path)
	fmt.Fprintf(&b, "- **Test:** `%s`\n", name)
	fmt.Fprintf(&b, "- **Failure rate:** %.1f%% over %d attempts in the last 30 days\n", rate*100, samples)
	b.WriteString("\n## What happened\n\n")
	b.WriteString("TEO's flake detector promoted this test to **quarantined** because its ")
	b.WriteString("Wilson 95% confidence-interval lower bound on failure rate exceeded 5%.\n\n")
	b.WriteString("The test will continue to run in a non-blocking lane: failures here do not fail the build.\n\n")
	b.WriteString("## Next steps\n\n")
	b.WriteString("1. Investigate the failure pattern in TEO's failure clusters page.\n")
	b.WriteString("2. Common causes: order-dependent state, async/timing, network, randomness, env-dependent.\n")
	b.WriteString("3. Once fixed, click 'Restore from quarantine' in the TEO UI.\n\n")
	b.WriteString("---\n")
	b.WriteString("_This issue was created automatically by [TEO](https://github.com/teo-dev/teo)._\n")
	return b.String()
}
