package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
