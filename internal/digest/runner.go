package digest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runner orchestrates a full digest pass: walk every enabled repo, build
// per-owner stats, render, resolve owner→email, and dispatch via the
// configured Sender. Per-user and per-repo opt-outs are enforced before
// rendering so we don't waste work on silenced recipients.
type Runner struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Sender Sender
}

// Result describes one Run pass for telemetry / dry-run output.
type Result struct {
	Repo       string
	Owner      string
	Email      string
	Skipped    bool
	SkipReason string
	SendError  string
	OwnedTests int
	FlakyTests int
	CIMinutes  float64
}

// Run performs one digest pass for every digest-enabled repo.
// Returns the per-recipient outcomes so callers (cron + dry-run) can log them.
func (r *Runner) Run(ctx context.Context) ([]Result, error) {
	if r.Pool == nil {
		return nil, errors.New("digest runner: pool required")
	}
	repos, err := r.eligibleRepos(ctx)
	if err != nil {
		return nil, err
	}

	var out []Result
	builder := &Builder{Pool: r.Pool}

	for _, rp := range repos {
		stats, err := builder.BuildAll(ctx, rp.id)
		if err != nil {
			r.Logger.Error("digest build failed", "repo", rp.full, "err", err)
			continue
		}
		for _, s := range stats {
			res := Result{
				Repo:       rp.full,
				Owner:      s.Owner,
				OwnedTests: s.OwnedTests,
				FlakyTests: s.FlakyTests,
				CIMinutes:  s.CIMinutes,
			}
			email, optOut := r.resolveOwner(ctx, s.Owner)
			if optOut {
				res.Skipped = true
				res.SkipReason = "user opted out"
				out = append(out, res)
				continue
			}
			res.Email = email

			html, err := RenderHTML(s)
			if err != nil {
				res.SkipReason = "render: " + err.Error()
				res.Skipped = true
				out = append(out, res)
				continue
			}
			text := RenderText(s)
			msg := Message{
				Owner:   s.Owner,
				Email:   email,
				Subject: fmt.Sprintf("TEO digest — %s (%s)", s.Owner, rp.full),
				HTML:    html,
				Text:    text,
			}
			if r.Sender == nil {
				out = append(out, res) // dry-run path
				continue
			}
			if err := r.Sender.Send(ctx, msg); err != nil {
				res.SendError = err.Error()
				r.Logger.Warn("digest send failed", "owner", s.Owner, "repo", rp.full, "err", err)
			}
			out = append(out, res)
		}
	}
	return out, nil
}

// RunForUser is the dry-run targeting one specific email. It does not require
// a Sender and does not enforce opt-out — useful for "show me what they'd see."
func (r *Runner) RunForUser(ctx context.Context, email string) ([]Message, error) {
	user, err := r.userByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	repos, err := r.eligibleRepos(ctx)
	if err != nil {
		return nil, err
	}
	var out []Message
	builder := &Builder{Pool: r.Pool}
	for _, rp := range repos {
		stats, err := builder.BuildAll(ctx, rp.id)
		if err != nil {
			continue
		}
		for _, s := range stats {
			if !ownerMatchesUser(s.Owner, user.email, user.handles) {
				continue
			}
			html, err := RenderHTML(s)
			if err != nil {
				return nil, err
			}
			out = append(out, Message{
				Owner:   s.Owner,
				Email:   email,
				Subject: fmt.Sprintf("TEO digest — %s (%s)", s.Owner, rp.full),
				HTML:    html,
				Text:    RenderText(s),
			})
		}
	}
	return out, nil
}

// --- DB helpers ------------------------------------------------------------

type repoRow struct {
	id   string
	full string
}

func (r *Runner) eligibleRepos(ctx context.Context) ([]repoRow, error) {
	rows, err := r.Pool.Query(ctx, `
        SELECT id::text, full_name
        FROM teo.repos
        WHERE enabled = TRUE
          AND COALESCE((meta->>'digest_opt_out')::bool, FALSE) = FALSE
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repoRow
	for rows.Next() {
		var r repoRow
		if err := rows.Scan(&r.id, &r.full); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type userRow struct {
	id      string
	email   string
	optOut  bool
	handles []string
}

// resolveOwner converts a CODEOWNERS owner identifier (e.g. "@org/team-billing"
// or "@alice") into an email address; returns optOut=true when the user has
// asked to be silenced. An owner that doesn't map to a known user is returned
// with empty email + optOut=false; the SMTP sender will skip silently.
//
// "Not found" is intentionally not an error — owners can move on/off teams,
// and the digest path treats unknown owners as "skip with empty email" rather
// than aborting the whole run. The signature returns (email, optOut) only.
func (r *Runner) resolveOwner(ctx context.Context, owner string) (email string, optOut bool) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return "", false
	}
	// Direct user match: @alice → alice
	if strings.HasPrefix(owner, "@") && !strings.Contains(owner, "/") {
		handle := strings.TrimPrefix(owner, "@")
		var u userRow
		err := r.Pool.QueryRow(ctx, `
            SELECT id::text, email, digest_opt_out
            FROM teo.users
            WHERE active = TRUE
              AND (lower(email) = lower($1) OR lower(split_part(email,'@',1)) = lower($1))
        `, handle).Scan(&u.id, &u.email, &u.optOut)
		if err != nil {
			// not found is not an error
			return "", false
		}
		return u.email, u.optOut
	}
	// Team match @org/team-* → no concrete user, leave email empty
	return "", false
}

func (r *Runner) userByEmail(ctx context.Context, email string) (userRow, error) {
	var u userRow
	err := r.Pool.QueryRow(ctx, `
        SELECT id::text, email, digest_opt_out
        FROM teo.users
        WHERE lower(email) = lower($1) AND active = TRUE
    `, email).Scan(&u.id, &u.email, &u.optOut)
	if err != nil {
		return userRow{}, err
	}
	// Pre-compute handle prefixes for ownerMatchesUser
	u.handles = []string{strings.ToLower(strings.SplitN(u.email, "@", 2)[0])}
	return u, nil
}

// ownerMatchesUser checks whether an owner identifier corresponds to a user.
// CODEOWNERS uses both individual handles ("@alice") and team handles
// ("@org/team-billing"); we match on individual handle in dry-run mode.
func ownerMatchesUser(owner, email string, handles []string) bool {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if owner == "" {
		return false
	}
	if strings.HasPrefix(owner, "@") && !strings.Contains(owner, "/") {
		h := strings.TrimPrefix(owner, "@")
		if h == strings.ToLower(strings.SplitN(email, "@", 2)[0]) {
			return true
		}
		for _, hh := range handles {
			if h == hh {
				return true
			}
		}
	}
	return false
}
