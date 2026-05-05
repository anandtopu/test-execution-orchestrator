package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/teo-dev/teo/internal/auth"
)

func TestSchemaHandlerHasExpectedTypes(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphql/schema", nil)
	schemaHandler().ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{"type Query", "Run", "FailureCluster", "FlakeRecord"} {
		if !strings.Contains(body, want) {
			t.Errorf("schema body missing %q", want)
		}
	}
}

// TestGraphqlHandlerRejectsRolelessPrincipal verifies the route-level 403 added
// alongside the mutation-role gate: an authenticated principal with no roles
// must not reach the resolvers. Anonymous → 401 is covered by the existing
// integration test in graphql_http_integration_test.go.
func TestGraphqlHandlerRejectsRolelessPrincipal(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql",
		strings.NewReader(`{"query":"{ runs { id } }"}`))
	ctx := auth.WithPrincipal(req.Context(), &auth.Principal{UserID: "x"})
	graphqlHandler(nil).ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Forbidden") {
		t.Fatalf("expected RFC 7807 Forbidden envelope, got %s", rr.Body.String())
	}
}
