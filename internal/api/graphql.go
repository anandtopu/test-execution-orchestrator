package api

import (
	"encoding/json"
	"net/http"

	"github.com/graphql-go/graphql"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/cost"
)

// graphqlHandler returns an http.Handler serving /graphql.
// Schema covers the read API surface used by the Web UI.
func graphqlHandler(pool *pgxpool.Pool) http.Handler {
	schema := buildSchema(pool)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.PrincipalFrom(r.Context()) == nil {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
			return
		}
		var req struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  req.Query,
			VariableValues: req.Variables,
			OperationName:  req.OperationName,
			Context:        r.Context(),
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// buildSchema declares Run/Shard/FailureCluster/FlakeRecord plus the root
// Query and Mutation. See E-09 strategy for the field rationale.
func buildSchema(pool *pgxpool.Pool) graphql.Schema {
	shardType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Shard",
		Fields: graphql.Fields{
			"id":                  &graphql.Field{Type: graphql.ID},
			"index":               &graphql.Field{Type: graphql.Int},
			"status":              &graphql.Field{Type: graphql.String},
			"workerId":            &graphql.Field{Type: graphql.String, Resolve: mapResolve("worker_id")},
			"predictedDurationMs": &graphql.Field{Type: graphql.Int, Resolve: mapResolve("predicted_duration_ms")},
			"actualDurationMs":    &graphql.Field{Type: graphql.Int, Resolve: mapResolve("actual_duration_ms")},
			"testCount":           &graphql.Field{Type: graphql.Int, Resolve: mapResolve("test_count")},
			"startedAt":           &graphql.Field{Type: graphql.String, Resolve: mapResolve("started_at")},
			"finishedAt":          &graphql.Field{Type: graphql.String, Resolve: mapResolve("finished_at")},
		},
	})
	runType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Run",
		Fields: graphql.Fields{
			"id":              &graphql.Field{Type: graphql.ID},
			"repoFullName":    &graphql.Field{Type: graphql.String, Resolve: mapResolve("repo_full_name")},
			"branch":          &graphql.Field{Type: graphql.String},
			"commitSha":       &graphql.Field{Type: graphql.String, Resolve: mapResolve("commit_sha")},
			"status":          &graphql.Field{Type: graphql.String},
			"totalDurationMs": &graphql.Field{Type: graphql.Int, Resolve: mapResolve("total_duration_ms")},
			"preemptionCount": &graphql.Field{Type: graphql.Int, Resolve: mapResolve("preemption_count")},
			"startedAt":       &graphql.Field{Type: graphql.String, Resolve: mapResolve("started_at")},
			"finishedAt":      &graphql.Field{Type: graphql.String, Resolve: mapResolve("finished_at")},
			"shards": &graphql.Field{
				Type: graphql.NewList(shardType),
				Resolve: func(p graphql.ResolveParams) (any, error) {
					m, ok := p.Source.(map[string]any)
					if !ok {
						return []any{}, nil
					}
					id, _ := m["id"].(string)
					if id == "" {
						return []any{}, nil
					}
					return queryShards(p.Context, pool, id)
				},
			},
			"failedTestCount": &graphql.Field{
				Type: graphql.Int,
				Resolve: func(p graphql.ResolveParams) (any, error) {
					m, ok := p.Source.(map[string]any)
					if !ok {
						return 0, nil
					}
					id, _ := m["id"].(string)
					if id == "" {
						return 0, nil
					}
					return queryFailedTestCount(p.Context, pool, id)
				},
			},
		},
	})
	failureClusterType := graphql.NewObject(graphql.ObjectConfig{
		Name: "FailureCluster",
		Fields: graphql.Fields{
			"id":                    &graphql.Field{Type: graphql.ID},
			"representativeMessage": &graphql.Field{Type: graphql.String, Resolve: mapResolve("representative_message")},
			"representativeStack":   &graphql.Field{Type: graphql.String, Resolve: mapResolve("representative_stack")},
			"occurrences":           &graphql.Field{Type: graphql.Int},
			"firstSeen":             &graphql.Field{Type: graphql.String, Resolve: mapResolve("first_seen")},
			"lastSeen":              &graphql.Field{Type: graphql.String, Resolve: mapResolve("last_seen")},
		},
	})
	flakeType := graphql.NewObject(graphql.ObjectConfig{
		Name: "FlakeRecord",
		Fields: graphql.Fields{
			"testId":      &graphql.Field{Type: graphql.ID, Resolve: mapResolve("test_id")},
			"testPath":    &graphql.Field{Type: graphql.String, Resolve: mapResolve("path")},
			"testName":    &graphql.Field{Type: graphql.String, Resolve: mapResolve("name")},
			"flakeRate":   &graphql.Field{Type: graphql.Float, Resolve: mapResolve("flake_rate")},
			"wilsonLower": &graphql.Field{Type: graphql.Float, Resolve: mapResolve("wilson_lower")},
			"sampleSize":  &graphql.Field{Type: graphql.Int, Resolve: mapResolve("sample_size")},
			"category":    &graphql.Field{Type: graphql.String},
		},
	})
	costWeekType := graphql.NewObject(graphql.ObjectConfig{
		Name: "CostWeek",
		Fields: graphql.Fields{
			"weekStart":       &graphql.Field{Type: graphql.String, Resolve: mapResolve("week_start")},
			"runs":            &graphql.Field{Type: graphql.Int},
			"spotMinutes":     &graphql.Field{Type: graphql.Float, Resolve: mapResolve("spot_minutes")},
			"onDemandMinutes": &graphql.Field{Type: graphql.Float, Resolve: mapResolve("ondemand_minutes")},
			"totalCost":       &graphql.Field{Type: graphql.Float, Resolve: mapResolve("total_cost")},
			"costPerBuild":    &graphql.Field{Type: graphql.Float, Resolve: mapResolve("cost_per_build")},
			"spotShare":       &graphql.Field{Type: graphql.Float, Resolve: mapResolve("spot_share")},
		},
	})
	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"runs": &graphql.Field{
				Type: graphql.NewList(runType),
				Args: graphql.FieldConfigArgument{
					"first": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					first := clampFirst(p.Args["first"])
					return queryRuns(p.Context, pool, first)
				},
			},
			"run": &graphql.Field{
				Type: runType,
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					id, _ := p.Args["id"].(string)
					if id == "" {
						return nil, nil
					}
					return queryRunByID(p.Context, pool, id)
				},
			},
			"failureClusters": &graphql.Field{
				Type: graphql.NewList(failureClusterType),
				Resolve: func(p graphql.ResolveParams) (any, error) {
					return queryFailureClusters(p.Context, pool)
				},
			},
			"flakes": &graphql.Field{
				Type: graphql.NewList(flakeType),
				Resolve: func(p graphql.ResolveParams) (any, error) {
					return queryFlakes(p.Context, pool)
				},
			},
			"costSummary": &graphql.Field{
				Type: graphql.NewList(costWeekType),
				Args: graphql.FieldConfigArgument{
					"weeks": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 8},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					weeks, _ := p.Args["weeks"].(int)
					pricer := cost.NewFromEnv()
					return queryCostSummary(p.Context, pool, pricer, weeks)
				},
			},
		},
	})

	rootMutation := graphql.NewObject(graphql.ObjectConfig{
		Name: "Mutation",
		Fields: graphql.Fields{
			"rerunFailed": &graphql.Field{
				Type: runType,
				Args: graphql.FieldConfigArgument{
					"runId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					runID, _ := p.Args["runId"].(string)
					if runID == "" {
						return nil, errInvalidRunID
					}
					return rerunFailed(p.Context, pool, runID)
				},
			},
		},
	})

	schema, _ := graphql.NewSchema(graphql.SchemaConfig{
		Query:    rootQuery,
		Mutation: rootMutation,
	})
	return schema
}

// clampFirst extracts and bounds the `first` arg to [1, 200].
func clampFirst(raw any) int {
	first, _ := raw.(int)
	if first <= 0 {
		return 50
	}
	if first > 200 {
		return 200
	}
	return first
}

// mapResolve pulls a key out of the source map (we resolve everything as
// map[string]any to avoid declaring a separate Go struct per row).
func mapResolve(key string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		if m, ok := p.Source.(map[string]any); ok {
			return m[key], nil
		}
		return nil, nil
	}
}
