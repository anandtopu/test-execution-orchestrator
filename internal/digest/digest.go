// Package digest builds the weekly per-author digest (FR-708).
// Aggregates last-week test outcomes per CODEOWNERS-resolved owner.
package digest

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OwnerStats summarizes one author's week.
type OwnerStats struct {
	Owner        string
	OwnedTests   int
	FlakyTests   int
	CIMinutes    float64
	WoWDeltaPct  float64 // ((this - prev) / prev) * 100
	SlowestTests []SlowTest
	GeneratedAt  time.Time
	WeekStart    time.Time
	WeekEnd      time.Time
}

// SlowTest is one row in the slowest-tests list.
type SlowTest struct {
	Path          string
	Name          string
	AvgDurationMS int
	Runs          int
}

// Builder loads stats from the DB and renders digests.
type Builder struct {
	Pool *pgxpool.Pool
}

// BuildAll returns one OwnerStats per owner with at least one owned test
// in the rolling window.
func (b *Builder) BuildAll(ctx context.Context, repoID string) ([]OwnerStats, error) {
	end := time.Now().UTC().Truncate(time.Hour)
	start := end.AddDate(0, 0, -7)
	prevStart := start.AddDate(0, 0, -7)

	rows, err := b.Pool.Query(ctx, `
        WITH this_week AS (
            SELECT t.owner_team, count(*) AS owned_tests,
                   sum(te.duration_ms)::float / 60000 AS minutes
            FROM teo.tests t
            JOIN teo.test_executions te ON te.test_id = t.id
            JOIN teo.shards s ON s.id = te.shard_id
            JOIN teo.runs r ON r.id = s.run_id
            WHERE r.repo_id = $1
              AND te.started_at BETWEEN $2 AND $3
              AND t.owner_team IS NOT NULL
            GROUP BY t.owner_team
        ),
        last_week AS (
            SELECT t.owner_team, sum(te.duration_ms)::float / 60000 AS prev_minutes
            FROM teo.tests t
            JOIN teo.test_executions te ON te.test_id = t.id
            JOIN teo.shards s ON s.id = te.shard_id
            JOIN teo.runs r ON r.id = s.run_id
            WHERE r.repo_id = $1
              AND te.started_at BETWEEN $4 AND $2
              AND t.owner_team IS NOT NULL
            GROUP BY t.owner_team
        ),
        flakes AS (
            SELECT t.owner_team, count(*) AS flaky_tests
            FROM teo.tests t
            JOIN teo.flake_records fr ON fr.test_id = t.id
            WHERE t.repo_id = $1
              AND fr.wilson_lower > 0.05
              AND t.owner_team IS NOT NULL
            GROUP BY t.owner_team
        )
        SELECT tw.owner_team,
               tw.owned_tests,
               COALESCE(f.flaky_tests, 0),
               tw.minutes,
               COALESCE(lw.prev_minutes, 0)
        FROM this_week tw
        LEFT JOIN last_week lw USING (owner_team)
        LEFT JOIN flakes f USING (owner_team)
    `, repoID, start, end, prevStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OwnerStats
	for rows.Next() {
		var s OwnerStats
		var prevMin float64
		if err := rows.Scan(&s.Owner, &s.OwnedTests, &s.FlakyTests, &s.CIMinutes, &prevMin); err != nil {
			return nil, err
		}
		if prevMin > 0 {
			s.WoWDeltaPct = ((s.CIMinutes - prevMin) / prevMin) * 100
		}
		s.WeekStart = start
		s.WeekEnd = end
		s.GeneratedAt = time.Now().UTC()
		// Slow tests
		slow, _ := b.slowestTests(ctx, repoID, s.Owner, start, end, 3)
		s.SlowestTests = slow
		out = append(out, s)
	}
	return out, rows.Err()
}

func (b *Builder) slowestTests(ctx context.Context, repoID, owner string, start, end time.Time, limit int) ([]SlowTest, error) {
	rows, err := b.Pool.Query(ctx, `
        SELECT t.path, t.name, avg(te.duration_ms)::int AS avg_ms, count(*)
        FROM teo.tests t
        JOIN teo.test_executions te ON te.test_id = t.id
        JOIN teo.shards s ON s.id = te.shard_id
        JOIN teo.runs r ON r.id = s.run_id
        WHERE t.repo_id = $1 AND t.owner_team = $2
          AND te.started_at BETWEEN $3 AND $4
        GROUP BY t.path, t.name
        ORDER BY avg_ms DESC
        LIMIT $5
    `, repoID, owner, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SlowTest
	for rows.Next() {
		var s SlowTest
		if err := rows.Scan(&s.Path, &s.Name, &s.AvgDurationMS, &s.Runs); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const digestHTMLTemplate = `<!doctype html>
<html><body style="font-family:system-ui,sans-serif;max-width:640px">
<h2>Your TEO digest — {{.Owner}}</h2>
<p>Week of {{.WeekStart.Format "2006-01-02"}} to {{.WeekEnd.Format "2006-01-02"}}.</p>
<ul>
<li><strong>{{.OwnedTests}}</strong> tests owned</li>
<li><strong>{{.FlakyTests}}</strong> flaky</li>
<li><strong>{{printf "%.1f" .CIMinutes}}</strong> min CI consumed
{{if ne .WoWDeltaPct 0.0}}({{if gt .WoWDeltaPct 0.0}}+{{end}}{{printf "%.1f" .WoWDeltaPct}}% WoW){{end}}</li>
</ul>
{{if .SlowestTests}}
<h3>Slowest tests</h3>
<ol>
{{range .SlowestTests}}<li><code>{{.Path}}::{{.Name}}</code> — {{.AvgDurationMS}} ms avg, {{.Runs}} runs</li>
{{end}}
</ol>
{{end}}
<hr>
<p style="color:#888;font-size:12px">
Generated {{.GeneratedAt.Format "2006-01-02 15:04"}} UTC. Manage preferences in TEO settings.
</p>
</body></html>`

// RenderHTML produces the HTML digest body.
func RenderHTML(s OwnerStats) (string, error) {
	tpl, err := template.New("digest").Parse(digestHTMLTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, s); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderText produces a plain-text digest body for fallback delivery.
func RenderText(s OwnerStats) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Your TEO digest — %s\n", s.Owner)
	fmt.Fprintf(&b, "Week of %s to %s\n\n", s.WeekStart.Format("2006-01-02"), s.WeekEnd.Format("2006-01-02"))
	fmt.Fprintf(&b, "  %d tests owned\n", s.OwnedTests)
	fmt.Fprintf(&b, "  %d flaky\n", s.FlakyTests)
	fmt.Fprintf(&b, "  %.1f min CI consumed", s.CIMinutes)
	if s.WoWDeltaPct != 0 {
		sign := "+"
		if s.WoWDeltaPct < 0 {
			sign = ""
		}
		fmt.Fprintf(&b, " (%s%.1f%% WoW)", sign, s.WoWDeltaPct)
	}
	b.WriteString("\n")
	if len(s.SlowestTests) > 0 {
		b.WriteString("\nSlowest tests:\n")
		for i, t := range s.SlowestTests {
			fmt.Fprintf(&b, "  %d. %s::%s — %d ms avg (%d runs)\n", i+1, t.Path, t.Name, t.AvgDurationMS, t.Runs)
		}
	}
	return b.String()
}
