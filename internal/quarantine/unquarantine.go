package quarantine

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UnquarantineProposer scans for quarantined tests that have passed K consecutive
// runs and posts a comment proposing un-quarantine, with a magic-link token.
// Per S-15-04: un-quarantine itself is operator-confirmed, never automatic.
type UnquarantineProposer struct {
	Pool                  *pgxpool.Pool
	Logger                *slog.Logger
	Commenter             IssueCommenter
	ConsecutivePassWindow int           // K: number of recent runs to inspect (default 30)
	TokenTTL              time.Duration // default 14 days
	BaseURL               string        // e.g. https://teo.example.com
}

// Run performs one sweep.
func (u *UnquarantineProposer) Run(ctx context.Context) error {
	if u.Pool == nil {
		return nil
	}
	if u.ConsecutivePassWindow <= 0 {
		u.ConsecutivePassWindow = 30
	}
	if u.TokenTTL == 0 {
		u.TokenTTL = 14 * 24 * time.Hour
	}

	// Find quarantined tests where the last K executions all passed AND we
	// haven't already proposed un-quarantine recently.
	rows, err := u.Pool.Query(ctx, `
        WITH ranked AS (
            SELECT te.test_id,
                   te.outcome,
                   row_number() OVER (PARTITION BY te.test_id ORDER BY te.started_at DESC) AS rn
            FROM teo.test_executions te
            JOIN teo.shards s ON s.id = te.shard_id
        ),
        recent_window AS (
            SELECT test_id,
                   bool_and(outcome = 'passed') AS all_passed,
                   count(*) AS n
            FROM ranked
            WHERE rn <= $1
            GROUP BY test_id
        )
        SELECT t.id, t.path, t.name, repos.full_name,
               fr.github_issue_number,
               fr.unquarantine_proposed_at
        FROM teo.flake_records fr
        JOIN teo.tests t ON t.id = fr.test_id
        JOIN teo.repos repos ON repos.id = t.repo_id
        JOIN recent_window rw ON rw.test_id = t.id
        WHERE t.status = 'quarantined'
          AND fr.github_issue_number IS NOT NULL
          AND rw.all_passed
          AND rw.n >= $1
          AND (fr.unquarantine_proposed_at IS NULL
               OR fr.unquarantine_proposed_at < now() - INTERVAL '7 days')
    `, u.ConsecutivePassWindow)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		testID, path, name, repoFull string
		issue                        int
	}
	var work []row
	for rows.Next() {
		var r row
		var proposed *time.Time
		if err := rows.Scan(&r.testID, &r.path, &r.name, &r.repoFull, &r.issue, &proposed); err != nil {
			return err
		}
		work = append(work, r)
	}

	for _, r := range work {
		token, err := u.mintToken(ctx, r.testID)
		if err != nil {
			u.Logger.Warn("token mint failed", "test", r.testID, "err", err)
			continue
		}
		body := fmt.Sprintf(
			"This test has passed **%d consecutive runs** since being quarantined.\n\n"+
				"It looks fixed. To restore it to the blocking lane, click:\n\n"+
				"  %s/api/v1/quarantine/restore?token=%s\n\n"+
				"_Restoration is operator-confirmed; this link is single-use and expires in %d days._\n\n"+
				"`%s::%s`",
			u.ConsecutivePassWindow,
			u.BaseURL,
			token,
			int(u.TokenTTL/(24*time.Hour)),
			r.path, r.name,
		)
		if u.Commenter != nil {
			if err := u.Commenter.Comment(ctx, r.repoFull, r.issue, body); err != nil {
				u.Logger.Warn("un-quarantine proposal comment failed", "issue", r.issue, "err", err)
				continue
			}
		}
		if _, err := u.Pool.Exec(ctx,
			`UPDATE teo.flake_records SET unquarantine_proposed_at = now() WHERE test_id = $1`,
			r.testID); err != nil {
			u.Logger.Warn("proposal state update failed", "test", r.testID, "err", err)
		}
	}
	u.Logger.Info("un-quarantine proposal sweep done", "candidates", len(work))
	return nil
}

func (u *UnquarantineProposer) mintToken(ctx context.Context, testID string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := u.Pool.Exec(ctx, `
        INSERT INTO teo.unquarantine_tokens (token, test_id, expires_at)
        VALUES ($1, $2, now() + ($3 || ' seconds')::interval)
    `, token, testID, fmt.Sprintf("%d", int(u.TokenTTL.Seconds()))); err != nil {
		return "", err
	}
	return token, nil
}

// Restore consumes a magic-link token and transitions the test to active.
// One-shot; subsequent calls with the same token return ErrTokenConsumed.
func Restore(ctx context.Context, pool *pgxpool.Pool, token string) (testID string, err error) {
	if token == "" {
		return "", errors.New("empty token")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var (
		consumed *time.Time
		expires  time.Time
	)
	if err := tx.QueryRow(ctx, `
        SELECT test_id, consumed_at, expires_at FROM teo.unquarantine_tokens WHERE token = $1 FOR UPDATE
    `, token).Scan(&testID, &consumed, &expires); err != nil {
		return "", err
	}
	if consumed != nil {
		return "", ErrTokenConsumed
	}
	if time.Now().After(expires) {
		return "", ErrTokenExpired
	}
	if _, err := tx.Exec(ctx,
		`UPDATE teo.unquarantine_tokens SET consumed_at = now() WHERE token = $1`, token); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
        UPDATE teo.tests SET status = 'active', quarantined_at = NULL, quarantine_reason = NULL
        WHERE id = $1
    `, testID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE teo.flake_records SET quarantined_at = NULL, unquarantined_at = now() WHERE test_id = $1`,
		testID); err != nil {
		return "", err
	}
	return testID, tx.Commit(ctx)
}

// ErrTokenConsumed indicates the magic-link token was already used.
var ErrTokenConsumed = errors.New("unquarantine token already consumed")

// ErrTokenExpired indicates the magic-link token is past its expires_at.
var ErrTokenExpired = errors.New("unquarantine token expired")
