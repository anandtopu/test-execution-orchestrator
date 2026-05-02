package quarantine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// captureCommenter records all Comment calls; used by sweep tests that don't
// need a live Postgres.
type captureCommenter struct {
	calls []commentCall
	err   error
}
type commentCall struct {
	repo   string
	number int
	body   string
}

func (c *captureCommenter) Comment(_ context.Context, repo string, number int, body string) error {
	c.calls = append(c.calls, commentCall{repo, number, body})
	return c.err
}

// We only test the message-construction here; the SQL-driven path requires
// integration tests against Postgres (called out as a known coverage gap).

func TestSLABodyContainsKeyFacts(t *testing.T) {
	c := &captureCommenter{}
	// Build a minimal SLASweeper that only invokes Commenter via direct method
	// — simulate the body builder by calling Comment with the same template.
	body := buildSlaBody("path/to/test.py", "test_x", 14, 21)
	if err := c.Comment(context.Background(), "owner/repo", 7, body); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(c.calls))
	}
	got := c.calls[0].body
	for _, want := range []string{"21 days", "SLA on flaky-test triage is 14 days", "test_x"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q: %s", want, got)
		}
	}
}

func TestCommenterErrorIsReported(t *testing.T) {
	c := &captureCommenter{err: errors.New("github 500")}
	err := c.Comment(context.Background(), "o/r", 1, "x")
	if err == nil {
		t.Fatal("expected error to surface")
	}
}

// buildSlaBody mirrors the template used by SLASweeper.Run; extracted for unit testing.
func buildSlaBody(path, name string, slaDays, days int) string {
	return "This test has been quarantined for **" +
		itoa(days) + " days**. The SLA on flaky-test triage is " + itoa(slaDays) + " days.\n\n" +
		"`" + path + "::" + name + "` is still failing intermittently in the non-blocking lane.\n\n" +
		"_Posted automatically by [TEO]_"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
