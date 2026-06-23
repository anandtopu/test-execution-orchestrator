package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"

	"github.com/teo-dev/teo/internal/auth"
)

// TestQuarantineMutationsExposedInSchema guards that the S-08-03 operator
// quarantine surface is wired into the programmatic schema.
func TestQuarantineMutationsExposedInSchema(t *testing.T) {
	schema := buildSchema(nil)
	m := schema.MutationType()
	if m == nil {
		t.Fatal("schema has no Mutation type")
	}
	for _, f := range []string{"quarantineTest", "unquarantineTest"} {
		if m.Fields()[f] == nil {
			t.Errorf("Mutation.%s missing", f)
		}
	}
	testT, ok := schema.Type("Test").(*graphql.Object)
	if !ok || testT == nil {
		t.Fatal("Test type not found")
	}
	for _, f := range []string{"id", "path", "name", "status", "quarantinedAt", "quarantineReason", "ownerTeam"} {
		if testT.Fields()[f] == nil {
			t.Errorf("Test.%s missing", f)
		}
	}
}

// TestQuarantineTestRequiresTestID exercises the resolver-layer arg guard with
// an engineer principal (so it reaches arg validation, not the role gate). A
// nil pool proves no DB call is made on the empty-id path.
func TestQuarantineTestRequiresTestID(t *testing.T) {
	for _, q := range []string{
		`mutation { quarantineTest(testId: "") { id } }`,
		`mutation { unquarantineTest(testId: "") { id } }`,
	} {
		res := graphql.Do(graphql.Params{
			Schema:        buildSchema(nil),
			Context:       engineerCtx(),
			RequestString: q,
		})
		if len(res.Errors) == 0 {
			t.Fatalf("expected error for empty testId in %q", q)
		}
		if !strings.Contains(res.Errors[0].Message, "testId is required") {
			t.Fatalf("expected testId-required error, got %q", res.Errors[0].Message)
		}
	}
}

// TestQuarantineMutationsRejectReadOnly locks in the role gate: even with a
// valid testId, a read-only principal is rejected before any DB call (nil pool).
func TestQuarantineMutationsRejectReadOnly(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(),
		&auth.Principal{Roles: []auth.Role{auth.RoleReadOnly}})
	for _, q := range []string{
		`mutation { quarantineTest(testId: "abc", reason: "flaky") { id } }`,
		`mutation { unquarantineTest(testId: "abc") { id } }`,
	} {
		res := graphql.Do(graphql.Params{
			Schema:        buildSchema(nil),
			Context:       ctx,
			RequestString: q,
		})
		if len(res.Errors) == 0 {
			t.Fatalf("expected forbidden error for read_only in %q", q)
		}
		if !strings.Contains(res.Errors[0].Message, "forbidden") {
			t.Fatalf("expected forbidden error, got %q", res.Errors[0].Message)
		}
	}
}

// TestSDLDeclaresQuarantineSurface checks the published SDL documents the new
// Test type and Mutation fields (the SDL-parity test only diffs read types, so
// this guards the mutation contract explicitly).
func TestSDLDeclaresQuarantineSurface(t *testing.T) {
	rr := httptest.NewRecorder()
	schemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/graphql/schema", nil))
	sdl := rr.Body.String()
	for _, want := range []string{
		"type Test {",
		"type Mutation {",
		"quarantineTest(testId: ID!, reason: String): Test",
		"unquarantineTest(testId: ID!): Test",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("published SDL missing %q", want)
		}
	}
}
