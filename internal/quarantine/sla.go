package quarantine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SLASweeper posts a "this has been open for N days" comment on every quarantine
// issue past its SLA, then sets last_nudged_at so the next sweep won't comment
// again until another SLA window has passed (S-15-03).
type SLASweeper struct {
	Pool      *pgxpool.Pool
	Logger    *slog.Logger
	Commenter IssueCommenter
	SLADays   int // default 14
}

// Run does one sweep.
func (s *SLASweeper) Run(ctx context.Context) error {
	if s.Pool == nil {
		return nil
	}
	if s.SLADays <= 0 {
		s.SLADays = 14
	}
	rows, err := s.Pool.Query(ctx, `
        SELECT t.id, t.path, t.name, repos.full_name,
               fr.github_issue_number, fr.quarantined_at,
               COALESCE(fr.last_nudged_at, fr.quarantined_at) AS last_action
        FROM teo.flake_records fr
        JOIN teo.tests t ON t.id = fr.test_id
        JOIN teo.repos repos ON repos.id = t.repo_id
        WHERE t.status = 'quarantined'
          AND fr.github_issue_number IS NOT NULL
          AND fr.quarantined_at IS NOT NULL
          AND now() - COALESCE(fr.last_nudged_at, fr.quarantined_at) > make_interval(days => $1)
    `, s.SLADays)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		testID, path, name, repoFull string
		issue                        int
		quarantinedAt                time.Time
		lastAction                   time.Time
	}
	var work []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.testID, &r.path, &r.name, &r.repoFull, &r.issue, &r.quarantinedAt, &r.lastAction); err != nil {
			return err
		}
		work = append(work, r)
	}

	for _, r := range work {
		if s.Commenter == nil {
			s.Logger.Warn("SLA sweep: no commenter configured", "test", r.testID)
			continue
		}
		days := int(time.Since(r.quarantinedAt).Hours() / 24)
		body := fmt.Sprintf(
			"This test has been quarantined for **%d days**. The SLA on flaky-test triage is %d days.\n\n"+
				"`%s::%s` is still failing intermittently in the non-blocking lane.\n\n"+
				"_Posted automatically by [TEO](https://github.com/teo-dev/teo); next nudge in %d days if no action._",
			days, s.SLADays, r.path, r.name, s.SLADays,
		)
		if err := s.Commenter.Comment(ctx, r.repoFull, r.issue, body); err != nil {
			s.Logger.Warn("SLA nudge failed", "test", r.testID, "issue", r.issue, "err", err)
			continue
		}
		if _, err := s.Pool.Exec(ctx,
			`UPDATE teo.flake_records SET last_nudged_at = now() WHERE test_id = $1`, r.testID); err != nil {
			s.Logger.Warn("SLA nudge state update failed", "test", r.testID, "err", err)
		}
	}
	s.Logger.Info("SLA sweep done", "candidates", len(work))
	return nil
}
