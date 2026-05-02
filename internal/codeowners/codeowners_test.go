package codeowners

import (
	"strings"
	"testing"
)

func TestBasicMatch(t *testing.T) {
	// Per GitHub's CODEOWNERS spec, later rules override earlier ones, so order
	// from least to most specific.
	o, err := Parse(strings.NewReader(`
# global default
*       @org/team-platform

# language-specific override
*.go    @org/team-go

# path-specific (most specific, listed last so it wins)
/services/billing/  @org/team-billing @alice
`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want []string
	}{
		{"services/billing/main.go", []string{"@org/team-billing", "@alice"}},
		{"misc/foo.go", []string{"@org/team-go"}},
		{"misc/foo.txt", []string{"@org/team-platform"}},
	}
	for _, c := range cases {
		got := o.Match(c.path)
		if !equal(got, c.want) {
			t.Errorf("path=%s got=%v want=%v", c.path, got, c.want)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
