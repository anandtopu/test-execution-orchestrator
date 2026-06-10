package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"

	"github.com/teo-dev/teo/internal/auth"
)

// engineerCtx returns a context carrying an engineer-role principal — the
// minimum credential needed to clear requireMutationRole and exercise the
// mutation's argument-validation path in tests.
func engineerCtx() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		UserID: "test-user",
		Roles:  []auth.Role{auth.RoleEngineer},
	})
}

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
	for _, f := range []string{
		"id", "shards", "failedTestCount", "preemptionCount", "totalDurationMs",
		// ui-home-calibration: flat run-level predictor aggregates the home
		// adapter reads first (before falling back to the nested predictor object).
		"predictorMae", "predictorRho", "modelVersion",
	} {
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
	for _, f := range []string{
		"id", "index", "status", "predictedDurationMs", "actualDurationMs", "testCount", "workerId", "deltaMs",
		// ui-home-calibration: per-shard calibration metadata for the home overlay.
		"predictionConfidence", "modelVersion",
	} {
		if st.Fields()[f] == nil {
			t.Errorf("Shard.%s missing", f)
		}
	}
}

func TestRunPredictorTypeExposesFields(t *testing.T) {
	schema := buildSchema(nil)
	pt, ok := schema.Type("RunPredictor").(*graphql.Object)
	if !ok || pt == nil {
		t.Fatal("RunPredictor type not found")
	}
	for _, f := range []string{"mae", "rho", "modelVersion", "p50DeltaMs", "p95DeltaMs", "sampleCount", "confidence"} {
		if pt.Fields()[f] == nil {
			t.Errorf("RunPredictor.%s missing", f)
		}
	}
	// Run must expose predictor wired to this type.
	runT := schema.Type("Run").(*graphql.Object)
	if runT.Fields()["predictor"] == nil {
		t.Error("Run.predictor missing")
	}
}

func TestFailureClusterTypeExposesFields(t *testing.T) {
	schema := buildSchema(nil)
	ct, ok := schema.Type("FailureCluster").(*graphql.Object)
	if !ok || ct == nil {
		t.Fatal("FailureCluster type not found")
	}
	for _, f := range []string{"id", "representativeMessage", "x", "y", "r", "category", "stackFingerprint", "affectedRuns"} {
		if ct.Fields()[f] == nil {
			t.Errorf("FailureCluster.%s missing", f)
		}
	}
}

func TestFlakeRecordTypeExposesFields(t *testing.T) {
	schema := buildSchema(nil)
	ft, ok := schema.Type("FlakeRecord").(*graphql.Object)
	if !ok || ft == nil {
		t.Fatal("FlakeRecord type not found")
	}
	// quarantinedAt + ownerTeam are the ui-clusters-flakes additions; the prior
	// fields are asserted alongside them to lock in the additive guarantee.
	for _, f := range []string{
		"testId", "testPath", "testName", "flakeRate", "wilsonLower", "wilsonUpper",
		"sampleSize", "category", "spark", "status", "durationMeanMs",
		"quarantinedAt", "ownerTeam",
	} {
		if ft.Fields()[f] == nil {
			t.Errorf("FlakeRecord.%s missing", f)
		}
	}
}

// TestFlakeRecordResolvesNewFieldsFromMapSource proves the quarantinedAt/ownerTeam
// resolvers read the snake_case keys queryFlakes emits, and that the pre-existing
// fields still resolve unchanged from the same stub source (additive guarantee).
func TestFlakeRecordResolvesNewFieldsFromMapSource(t *testing.T) {
	schema := buildSchema(nil)
	flakeT := schema.Type("FlakeRecord").(*graphql.Object)
	queryRoot := graphql.NewObject(graphql.ObjectConfig{
		Name: "Q",
		Fields: graphql.Fields{
			"f": &graphql.Field{
				Type: flakeT,
				Resolve: func(_ graphql.ResolveParams) (any, error) {
					return map[string]any{
						"test_id":          "t-abc",
						"path":             "pkg/foo_test.go",
						"name":             "TestFoo",
						"flake_rate":       0.2,
						"wilson_lower":     0.1,
						"wilson_upper":     0.35,
						"sample_size":      100,
						"category":         "race",
						"spark":            "PPFPPPPPPPPPPPPPPPPP",
						"status":           "quarantined",
						"duration_mean_ms": 1500,
						"quarantined_at":   "2026-06-01T00:00:00Z",
						"owner_team":       "@teo-dev/platform",
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
		RequestString: `{ f { testId testPath wilsonUpper status durationMeanMs quarantinedAt ownerTeam } }`,
	})
	if len(res.Errors) > 0 {
		t.Fatalf("graphql errors: %v", res.Errors)
	}
	out, _ := json.Marshal(res.Data)
	for _, want := range []string{
		`"testId":"t-abc"`,
		`"testPath":"pkg/foo_test.go"`,
		`"wilsonUpper":0.35`,
		`"status":"quarantined"`,
		`"durationMeanMs":1500`,
		`"quarantinedAt":"2026-06-01T00:00:00Z"`,
		`"ownerTeam":"@teo-dev/platform"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("response missing %q; got: %s", want, string(out))
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

// sdlTypeFields extracts the field names declared inside `type <name> { ... }`
// in the hand-written SDL body. Best-effort line parser (field names are the
// leading identifier before a ':') — good enough to diff field SETS against the
// programmatic schema.
func sdlTypeFields(sdl, typeName string) map[string]bool {
	out := map[string]bool{}
	start := strings.Index(sdl, "type "+typeName+" {")
	if start < 0 {
		return out
	}
	body := sdl[start:]
	open := strings.Index(body, "{")
	closeIdx := strings.Index(body, "}")
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return out
	}
	for _, line := range strings.Split(body[open+1:closeIdx], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// field name is the token up to the first '(' (args) or ':'.
		name := line
		if i := strings.IndexAny(name, "(:"); i >= 0 {
			name = name[:i]
		}
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// TestSDLAgreesWithProgrammaticSchema guards the published SDL (schemaHandler)
// against the programmatic schema (buildSchema) drifting apart — a field added
// to one but forgotten in the other would ship a wrong contract to codegen
// consumers with no other failing test. We diff field SETS for the read types
// the UI depends on.
func TestSDLAgreesWithProgrammaticSchema(t *testing.T) {
	rr := httptest.NewRecorder()
	schemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/graphql/schema", nil))
	sdl := rr.Body.String()

	schema := buildSchema(nil)
	for _, typeName := range []string{"Run", "Shard", "RunPredictor", "FailureCluster", "FlakeRecord"} {
		obj, ok := schema.Type(typeName).(*graphql.Object)
		if !ok || obj == nil {
			t.Fatalf("programmatic schema missing type %q", typeName)
		}
		sdlFields := sdlTypeFields(sdl, typeName)
		if len(sdlFields) == 0 {
			t.Fatalf("SDL has no fields parsed for type %q", typeName)
		}
		// Every programmatic field must be in the SDL.
		for f := range obj.Fields() {
			if !sdlFields[f] {
				t.Errorf("%s.%s present in buildSchema but missing from published SDL", typeName, f)
			}
		}
		// Every SDL field must be in the programmatic schema.
		for f := range sdlFields {
			if obj.Fields()[f] == nil {
				t.Errorf("%s.%s present in published SDL but missing from buildSchema", typeName, f)
			}
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
// resolver layer (defense in depth — graphql.NonNull also catches it). An
// engineer-role principal is injected so the test exercises the runId path
// rather than the role gate added in 2026-05-05.
func TestRerunFailedRequiresRunID(t *testing.T) {
	schema := buildSchema(nil)
	res := graphql.Do(graphql.Params{
		Schema:        schema,
		Context:       engineerCtx(),
		RequestString: `mutation { rerunFailed(runId: "") { id } }`,
	})
	if len(res.Errors) == 0 {
		t.Fatal("expected error for empty runId")
	}
	if !strings.Contains(res.Errors[0].Message, "runId is required") {
		t.Fatalf("expected runId-required error, got %q", res.Errors[0].Message)
	}
}

// TestRequireMutationRole covers the gate directly, decoupled from any
// resolver. Read-only and unauthenticated principals are forbidden; engineers
// and admins pass.
func TestRequireMutationRole(t *testing.T) {
	cases := []struct {
		name   string
		ctx    context.Context
		wantOK bool
	}{
		{name: "no_principal", ctx: context.Background(), wantOK: false},
		{name: "read_only", ctx: auth.WithPrincipal(context.Background(),
			&auth.Principal{Roles: []auth.Role{auth.RoleReadOnly}}), wantOK: false},
		{name: "engineer", ctx: auth.WithPrincipal(context.Background(),
			&auth.Principal{Roles: []auth.Role{auth.RoleEngineer}}), wantOK: true},
		{name: "admin", ctx: auth.WithPrincipal(context.Background(),
			&auth.Principal{Roles: []auth.Role{auth.RoleAdmin}}), wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireMutationRole(tc.ctx)
			if tc.wantOK && err != nil {
				t.Fatalf("expected nil err, got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("expected forbidden, got nil")
			}
		})
	}
}

// TestRerunFailedRejectsReadOnlyPrincipal locks in the resolver-layer guard:
// even with a valid runId arg, a read-only principal must be rejected before
// any DB call is made (proven by passing a nil pool).
func TestRerunFailedRejectsReadOnlyPrincipal(t *testing.T) {
	schema := buildSchema(nil)
	ctx := auth.WithPrincipal(context.Background(),
		&auth.Principal{Roles: []auth.Role{auth.RoleReadOnly}})
	res := graphql.Do(graphql.Params{
		Schema:        schema,
		Context:       ctx,
		RequestString: `mutation { rerunFailed(runId: "abc") { id } }`,
	})
	if len(res.Errors) == 0 {
		t.Fatal("expected forbidden error for read_only")
	}
	if !strings.Contains(res.Errors[0].Message, "forbidden") {
		t.Fatalf("expected forbidden error, got %q", res.Errors[0].Message)
	}
}
