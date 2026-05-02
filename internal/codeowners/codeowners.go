// Package codeowners parses GitHub-style CODEOWNERS files and matches paths
// to owners.
package codeowners

import (
	"bufio"
	"io"
	"path"
	"path/filepath"
	"strings"
)

// Rule is one CODEOWNERS line: a glob pattern + owners.
type Rule struct {
	Pattern string
	Owners  []string
}

// Owners is an ordered list of CODEOWNERS rules; later rules override earlier
// ones (GitHub semantics).
type Owners struct {
	Rules []Rule
}

// Parse reads CODEOWNERS content into an Owners struct.
func Parse(r io.Reader) (*Owners, error) {
	o := &Owners{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		o.Rules = append(o.Rules, Rule{
			Pattern: fields[0],
			Owners:  fields[1:],
		})
	}
	return o, sc.Err()
}

// Match returns the owners for the given file path, or nil if no rule matches.
// Later rules win — we walk in reverse and return the first match.
func (o *Owners) Match(filepath string) []string {
	for i := len(o.Rules) - 1; i >= 0; i-- {
		if matchPattern(o.Rules[i].Pattern, filepath) {
			return o.Rules[i].Owners
		}
	}
	return nil
}

// matchPattern implements the subset of GitHub CODEOWNERS glob we need:
// - "*"                       any single path segment
// - "*.ext"                   match basename ending in .ext (anywhere)
// - "/foo/bar/baz.go"         absolute path
// - "/dir/"                   directory prefix
// - "**"                      any path
// - leading "**/" or "/**/"   any depth
func matchPattern(pat, p string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return false
	}
	p = strings.TrimPrefix(p, "/")

	// Pure wildcard
	if pat == "*" || pat == "**" {
		return true
	}

	// Anchored absolute paths and directory prefixes
	if anchored, ok := strings.CutPrefix(pat, "/"); ok {
		// Directory prefix: "/dir/"
		if strings.HasSuffix(anchored, "/") {
			return strings.HasPrefix(p, anchored)
		}
		// Trailing /** or /*
		if prefix, ok := strings.CutSuffix(anchored, "/**"); ok {
			return p == prefix || strings.HasPrefix(p, prefix+"/")
		}
		if strings.Contains(anchored, "*") {
			ok, _ := path.Match(anchored, p)
			return ok
		}
		return p == anchored
	}

	// Pattern with no leading slash: matches at any depth
	// Special-case "*.ext"
	if strings.HasPrefix(pat, "*.") && !strings.ContainsAny(pat[1:], "/*") {
		return strings.HasSuffix(filepath.Base(p), pat[1:])
	}

	// Generic case: try matching against any suffix of the path.
	if strings.Contains(pat, "/") {
		segments := strings.Split(p, "/")
		for i := range segments {
			candidate := strings.Join(segments[i:], "/")
			ok, _ := path.Match(pat, candidate)
			if ok {
				return true
			}
		}
		return false
	}
	// Single-segment pattern matches the basename
	ok, _ := path.Match(pat, filepath.Base(p))
	return ok
}
