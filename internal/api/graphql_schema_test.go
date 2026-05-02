package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"
)

// buildSchema requires a *pgxpool.Pool but for schema-shape tests we never
// invoke the resolvers that touch DB; passing nil is safe as long as the
// query we run only inspects metadata or hits a stubbed Source.

func TestSchemaParsesAndExposesAllRootFields(t *testing.T) {
	schema := buildSchema(nil)
	q := schema.QueryType()
	want := []string{"runs", "run", "failureClusters", "flakes"}
	for _, f := range want {
		if q.Fields()[f] == nil {
			t.Errorf("Query.%s missing", f)
		}
	}
	m := schema.MutationType()
	if m == nil || m.Fields()["rerunFailed"] == nil {
		t.Errorf("Mutation.rerunFailed missing")
	}
}

func TestRunTypeExposesShardsAndFailedCount(t *testing.T) {
	schema := buildSchema(nil)
	runT, ok := schema.Type("Run").(*graphql.Object)
	if !ok || runT == nil {
		t.Fatal("Run type not found")
	}
	for _, f := range []string{"id", "shards", "failedTestCount", "preemptionCount", "totalDurationMs"} {
		if runT.Fields()[f] == nil {
			t.Errorf("Run.%s missing", f)
		}
	}
}

func TestShardTypeExposesExpectedFields(t *testing.T) {
	schema := buildSchema(nil)
	st, ok := schema.Type("Shard").(*graphql.Object)
	if !ok || st == nil {
		t.Fatal("Shard type not found")
	}
	for _, f := range []string{"id", "index", "status", "predictedDurationMs", "actualDurationMs", "testCount", "workerId"} {
		if st.Fields()[f] == nil {
			t.Errorf("Shard.%s missing", f)
		}
	}
}

func TestClampFirst(t *testing.T) {
	cases := map[string]struct {
		in   any
		want int
	}{
		"zero":     {0, 50},
		"negative": {-5, 50},
		"normal":   {10, 10},
		"max":      {200, 200},
		"over":     {500, 200},
		"nil":      {nil, 50},
	}
	for name, c := range cases {
		if got := clampFirst(c.in); got != c.want {
			t.Errorf("%s: clampFirst(%v) = %d, want %d", name, c.in, got, c.want)
		}
	}
}

func TestSchemaHandlerStillReturnsSDL(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphql/schema", nil)
	schemaHandler().ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{"type Query", "type Run", "type FailureCluster", "type FlakeRecord"} {
		if !strings.Contains(body, want) {
			t.Errorf("schema body missing %q", want)
		}
	}
}

// TestRunTypeResolvesFromMapSource verifies the Run object resolves its scalar
// fields from a stub map[string]any source — i.e., the mapResolve plumbing
// matches the keys queryRuns/queryRunByID emit.
func TestRunTypeResolvesFromMapSource(t *testing.T) {
	schema := buildSchema(nil)
	// Bypass root Query resolution by serializing a Run literal and reading it
	// back via introspection. Easier approach: use graphql.Do with a stubbed
	// resolver via a custom schema wrapping the Run type as a query field.

	tmpRun := schema.Type("Run").(*graphql.Object)
	queryRoot := graphql.NewObject(graphql.ObjectConfig{
		Name: "Q",
		Fields: graphql.Fields{
			"r": &graphql.Field{
				Type: tmpRun,
				Resolve: func(_ graphql.ResolveParams) (any, error) {
					return map[string]any{
						"id":                "abc",
						"repo_full_name":    "owner/repo",
						"commit_sha":        "deadbeef",
						"branch":            "main",
						"status":            "succeeded",
						"total_duration_ms": 12345,
						"preemption_count":  2,
						"started_at":        "2026-04-30T00:00:00Z",
						"finished_at":       "2026-04-30T00:01:00Z",
					}, nil
				},
			},
		},
	})
	s, err := graphql.NewSchema(graphql.SchemaConfig{Query: queryRoot})
	if err != nil {
		t.Fatal(err)
	}
	res := graphql.Do(graphql.Params{
		Schema:        s,
		Context:       context.Background(),
		RequestString: `{ r { id repoFullName branch commitSha status totalDurationMs preemptionCount startedAt finishedAt } }`,
	})
	if len(res.Errors) > 0 {
		t.Fatalf("graphql errors: %v", res.Errors)
	}
	out, _ := json.Marshal(res.Data)
	for _, want := range []string{
		`"id":"abc"`,
		`"repoFullName":"owner/repo"`,
		`"commitSha":"deadbeef"`,
		`"status":"succeeded"`,
		`"totalDurationMs":12345`,
		`"preemptionCount":2`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("response missing %q; got: %s", want, string(out))
		}
	}
}

// TestRerunFailedRequiresRunID verifies the mutation rejects empty IDs at the
// resolver layer (defense in depth — graphql.NonNull also catches it).
func TestRerunFailedRequiresRunID(t *testing.T) {
	schema := buildSchema(nil)
	res := graphql.Do(graphql.Params{
		Schema:        schema,
		Context:       context.Background(),
		RequestString: `mutation { rerunFailed(runId: "") { id } }`,
	})
	if len(res.Errors) == 0 {
		t.Fatal("expected error for empty runId")
	}
}
