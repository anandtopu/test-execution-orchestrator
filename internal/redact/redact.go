// Package redact scrubs known secret patterns from log streams before they
// leave the worker (ADR-0016). Replacement is `[REDACTED:<rule_id>]` so a
// triage user sees that something fired and which rule.
package redact

import (
	"bytes"
	"io"
	"regexp"
)

// Rule describes one redaction pattern.
type Rule struct {
	ID      string
	Pattern *regexp.Regexp
}

// Redactor applies rules to byte streams.
type Redactor struct {
	Rules []Rule
}

// DefaultRules ships with TEO. Operators can append via Helm values.
func DefaultRules() []Rule {
	return []Rule{
		{ID: "aws_access_key", Pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
		{ID: "aws_temp_key", Pattern: regexp.MustCompile(`ASIA[0-9A-Z]{16}`)},
		// JWTs (header.payload.sig)
		{ID: "jwt", Pattern: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
		// GitHub PATs
		{ID: "gh_pat_v2", Pattern: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82}`)},
		{ID: "gh_pat_v1", Pattern: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
		// Stripe live keys
		{ID: "stripe_live", Pattern: regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`)},
		// Generic 40-char base64 near "SECRET" (heuristic for AWS secret keys, etc.)
		{ID: "high_entropy_secret",
			Pattern: regexp.MustCompile(`(?i)secret[^=:]{0,16}[=:]\s*['"]?[A-Za-z0-9/+=]{40,}['"]?`)},
	}
}

// New returns a Redactor preloaded with DefaultRules.
func New() *Redactor {
	return &Redactor{Rules: DefaultRules()}
}

// Apply returns the redacted form of in.
func (r *Redactor) Apply(in []byte) []byte {
	out := in
	for _, rule := range r.Rules {
		repl := []byte("[REDACTED:" + rule.ID + "]")
		out = rule.Pattern.ReplaceAll(out, repl)
	}
	return out
}

// String redacts a string.
func (r *Redactor) String(s string) string {
	return string(r.Apply([]byte(s)))
}

// Wrap returns an io.Writer that redacts on the way through.
// Note: this redacts in fixed-size buffers; multi-byte secrets that straddle
// a Write boundary are handled by accumulating up to maxBuf bytes before flushing.
func (r *Redactor) Wrap(w io.Writer) io.Writer {
	return &writer{r: r, w: w, buf: bytes.NewBuffer(nil), maxBuf: 32 * 1024}
}

type writer struct {
	r      *Redactor
	w      io.Writer
	buf    *bytes.Buffer
	maxBuf int
}

func (w *writer) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if w.buf.Len() < w.maxBuf {
		return len(p), nil
	}
	if err := w.flush(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *writer) flush(final bool) error {
	if w.buf.Len() == 0 {
		return nil
	}
	// keep the tail in case a pattern crosses the boundary; final=true flushes everything
	tailKeep := 256
	var redacted []byte
	if final || w.buf.Len() <= tailKeep {
		redacted = w.r.Apply(w.buf.Bytes())
		w.buf.Reset()
	} else {
		body := w.buf.Bytes()[:w.buf.Len()-tailKeep]
		tail := w.buf.Bytes()[w.buf.Len()-tailKeep:]
		redacted = w.r.Apply(body)
		w.buf.Reset()
		w.buf.Write(tail)
	}
	_, err := w.w.Write(redacted)
	return err
}

// Close flushes the remaining buffer.
func (w *writer) Close() error {
	return w.flush(true)
}
