package api

import (
	"context"
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
		p := auth.PrincipalFrom(r.Context())
		if p == nil {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
			return
		}
		if len(p.Roles) == 0 {
			writeProblem(w, http.StatusForbidden, "Forbidden", "principal has no role")
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
			// deltaMs = actual-predicted, computed from the row so the calibration
			// overlay doesn't recompute. Null when actual is missing.
			"deltaMs": &graphql.Field{Type: graphql.Int, Resolve: shardDeltaResolve},
			// ui-home-calibration: per-shard predictor calibration metadata so the
			// home overlay is a genuine predicted-vs-observed view. These resolve to
			// null today because queryShards does NOT yet select
			// prediction_confidence / model_version (mapResolve returns nil for an
			// absent map key → GraphQL null). When the sibling migration
			// (graphql-schema-fields gap) adds teo.shards.prediction_confidence +
			// model_version, queryShards' SELECT + scanToMaps column list MUST be
			// updated to read them — they do NOT light up automatically.
			"predictionConfidence": &graphql.Field{Type: graphql.Float, Resolve: mapResolve("prediction_confidence")},
			"modelVersion":         &graphql.Field{Type: graphql.String, Resolve: mapResolve("model_version")},
		},
	})
	runPredictorType := graphql.NewObject(graphql.ObjectConfig{
		Name: "RunPredictor",
		Fields: graphql.Fields{
			"mae":          &graphql.Field{Type: graphql.Float},
			"rho":          &graphql.Field{Type: graphql.Float},
			"modelVersion": &graphql.Field{Type: graphql.String, Resolve: mapResolve("model_version")},
			"p50DeltaMs":   &graphql.Field{Type: graphql.Int, Resolve: mapResolve("p50_delta_ms")},
			"p95DeltaMs":   &graphql.Field{Type: graphql.Int, Resolve: mapResolve("p95_delta_ms")},
			"sampleCount":  &graphql.Field{Type: graphql.Int, Resolve: mapResolve("sample_count")},
			"confidence":   &graphql.Field{Type: graphql.Float},
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
			"predictor": &graphql.Field{
				Type: runPredictorType,
				Resolve: func(p graphql.ResolveParams) (any, error) {
					m, ok := p.Source.(map[string]any)
					if !ok {
						return nil, nil
					}
					id, _ := m["id"].(string)
					if id == "" {
						return nil, nil
					}
					// Return an UNTYPED nil for the <2-finished-shards case.
					// cachedRunPredictor returns a typed nil map[string]any there;
					// handing that to graphql-go as `any` yields a non-nil
					// interface, which it renders as an all-null object instead of
					// a null predictor. Collapse it to a real nil so the field
					// resolves to null per the documented contract.
					pred, err := cachedRunPredictor(p.Context, pool, m, id)
					if err != nil || pred == nil {
						return nil, err
					}
					return pred, nil
				},
			},
			// ui-home-calibration: flat run-level predictor aggregates so the home
			// adapter can read mae/rho/modelVersion without traversing the nested
			// predictor object. They share queryRunPredictor's result (computed from
			// the run's finished shards) and degrade to null when <2 finished shards.
			"predictorMae": &graphql.Field{Type: graphql.Float, Resolve: runPredictorFieldResolve(pool, "mae")},
			"predictorRho": &graphql.Field{Type: graphql.Float, Resolve: runPredictorFieldResolve(pool, "rho")},
			"modelVersion": &graphql.Field{Type: graphql.String, Resolve: runPredictorFieldResolve(pool, "model_version")},
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
			// Spatial-map fields for the Clusters screen, computed server-side in
			// computeClusterLayout (presentation-only, relative to the page).
			"x":                &graphql.Field{Type: graphql.Float},
			"y":                &graphql.Field{Type: graphql.Float},
			"r":                &graphql.Field{Type: graphql.Float},
			"category":         &graphql.Field{Type: graphql.String},
			"stackFingerprint": &graphql.Field{Type: graphql.String, Resolve: mapResolve("stack_fingerprint")},
			"affectedRuns":     &graphql.Field{Type: graphql.Int, Resolve: mapResolve("affected_runs")},
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
			"wilsonUpper": &graphql.Field{Type: graphql.Float, Resolve: mapResolve("wilson_upper")},
			"sampleSize":  &graphql.Field{Type: graphql.Int, Resolve: mapResolve("sample_size")},
			"category":    &graphql.Field{Type: graphql.String},
			// Sparkline (last-20 P/F/S outcomes, chronological), status badge, and
			// mean duration for the Flakes screen.
			"spark":          &graphql.Field{Type: graphql.String},
			"status":         &graphql.Field{Type: graphql.String},
			"durationMeanMs": &graphql.Field{Type: graphql.Int, Resolve: mapResolve("duration_mean_ms")},
			// quarantinedAt (ISO timestamp the test was quarantined, null when
			// not quarantined) and ownerTeam (CODEOWNERS-resolved owning team)
			// power the Flakes screen's quarantine-day count + owner avatar.
			"quarantinedAt": &graphql.Field{Type: graphql.String, Resolve: mapResolve("quarantined_at")},
			"ownerTeam":     &graphql.Field{Type: graphql.String, Resolve: mapResolve("owner_team")},
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
					if err := requireMutationRole(p.Context); err != nil {
						return nil, err
					}
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

// shardDeltaResolve computes actual-predicted (ms) from a shard row. Returns
// nil when either value is absent so GraphQL emits null rather than a bogus 0.
func shardDeltaResolve(p graphql.ResolveParams) (any, error) {
	m, ok := p.Source.(map[string]any)
	if !ok {
		return nil, nil
	}
	actual, aok := toInt64(m["actual_duration_ms"])
	if !aok {
		return nil, nil
	}
	predicted, _ := toInt64(m["predicted_duration_ms"])
	return int(actual - predicted), nil
}

// toInt64 coerces the numeric types pgx hands back (int64/int32/int) to int64.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

// runPredictorFieldResolve resolves a single flat run-level predictor field
// (predictorMae/predictorRho/modelVersion) by delegating to queryRunPredictor
// and pulling `key` out of its result map. Returns nil when the run has too few
// finished shards to compute calibration, so GraphQL emits null and the home
// overlay degrades to an em-dash rather than a bogus 0.
func runPredictorFieldResolve(pool *pgxpool.Pool, key string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		m, ok := p.Source.(map[string]any)
		if !ok {
			return nil, nil
		}
		id, _ := m["id"].(string)
		if id == "" {
			return nil, nil
		}
		pred, err := cachedRunPredictor(p.Context, pool, m, id)
		if err != nil || pred == nil {
			return nil, err
		}
		return pred[key], nil
	}
}

// runPredictorCacheKey is the private key under which the computed predictor map
// is stashed on the run Source map for the duration of a single request. The
// run map[string]any is the SAME instance passed to every field resolver of a
// given run within one graphql.Do, so caching on it memoizes queryRunPredictor
// per (request, run).
const runPredictorCacheKey = "__predictorCache"

// cachedRunPredictor memoizes queryRunPredictor on the run Source map so the
// nested predictor{...} resolver and the three flat field resolvers
// (predictorMae/predictorRho/modelVersion) share ONE computation instead of
// each issuing the 2 DB queries independently (4x fan-out = 8 round-trips per
// run on every 2s poll). Both the result and the (nil) "computed" sentinel are
// cached so a run with <2 finished shards isn't recomputed by each flat field.
//
// graphql-go resolves a run's fields sequentially (no concurrent field
// resolution within one object), so a plain map stash needs no locking here.
func cachedRunPredictor(ctx context.Context, pool *pgxpool.Pool, source map[string]any, id string) (map[string]any, error) {
	if cached, ok := source[runPredictorCacheKey]; ok {
		// nil is a legitimate cached value (too few finished shards).
		pred, _ := cached.(map[string]any)
		return pred, nil
	}
	pred, err := queryRunPredictor(ctx, pool, id)
	if err != nil {
		return nil, err
	}
	source[runPredictorCacheKey] = pred
	return pred, nil
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
