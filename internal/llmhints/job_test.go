package llmhints

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubSource struct {
	pending     []Cluster
	saved       []Hint
	saveErr     error
	lastRestale bool
}

func (s *stubSource) PendingClusters(_ context.Context, _ bool, _ int) ([]Cluster, error) {
	return s.pending, nil
}

func (s *stubSource) SaveHint(_ context.Context, h Hint, restale bool) error {
	s.lastRestale = restale
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, h)
	return nil
}

type stubSummarizer struct {
	hints map[string]Hint
	err   error
}

func (s stubSummarizer) Summarize(_ context.Context, clusters []Cluster) ([]Hint, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []Hint
	for _, c := range clusters {
		if h, ok := s.hints[c.ID]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func clusters(ids ...string) []Cluster {
	out := make([]Cluster, len(ids))
	for i, id := range ids {
		out[i] = Cluster{ID: id, RepoID: "r", Message: "boom", Stack: "stack"}
	}
	return out
}

func TestRunnerHappyPathSavesEveryHint(t *testing.T) {
	src := &stubSource{pending: clusters("c1", "c2")}
	sum := stubSummarizer{hints: map[string]Hint{
		"c1": {ClusterID: "c1", Hint: "h1", Category: "assertion", Confidence: 0.9},
		"c2": {ClusterID: "c2", Hint: "h2", Category: "timeout", Confidence: 0.5},
	}}
	r := &Runner{Clusters: src, Summarizer: sum}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, stats.Scanned)
	require.Equal(t, 2, stats.Hinted)
	require.Equal(t, 0, stats.Skipped)
	require.Equal(t, 0, stats.Errors)
	require.Len(t, src.saved, 2)
}

func TestRunnerSkipsClusterWithoutUsableHint(t *testing.T) {
	src := &stubSource{pending: clusters("c1", "c2")}
	// Summarizer returns a hint for c1 only; c2 has no entry.
	sum := stubSummarizer{hints: map[string]Hint{
		"c1": {ClusterID: "c1", Hint: "h1"},
	}}
	r := &Runner{Clusters: src, Summarizer: sum}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Hinted)
	require.Equal(t, 1, stats.Skipped)
	require.Len(t, src.saved, 1)
	require.Equal(t, "c1", src.saved[0].ClusterID)
}

func TestRunnerEmptyHintStringIsSkipped(t *testing.T) {
	src := &stubSource{pending: clusters("c1")}
	sum := stubSummarizer{hints: map[string]Hint{
		"c1": {ClusterID: "c1", Hint: ""}, // present but empty -> not usable
	}}
	r := &Runner{Clusters: src, Summarizer: sum}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, stats.Hinted)
	require.Equal(t, 1, stats.Skipped)
	require.Empty(t, src.saved)
}

func TestRunnerDryRunDoesNotPersist(t *testing.T) {
	src := &stubSource{pending: clusters("c1", "c2")}
	sum := stubSummarizer{hints: map[string]Hint{
		"c1": {ClusterID: "c1", Hint: "h1"},
		"c2": {ClusterID: "c2", Hint: "h2"},
	}}
	r := &Runner{Clusters: src, Summarizer: sum, DryRun: true}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.True(t, stats.DryRun)
	require.Equal(t, 2, stats.Hinted)
	require.Empty(t, src.saved, "dry-run must not persist")
}

func TestRunnerBatchErrorCountedAndContinues(t *testing.T) {
	src := &stubSource{pending: clusters("c1", "c2", "c3")}
	sum := stubSummarizer{err: errors.New("api down")}
	// Batch size 2 -> two batches (2 + 1); both fail, none aborts the pass.
	r := &Runner{Clusters: src, Summarizer: sum, BatchSize: 2}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, stats.Scanned)
	require.Equal(t, 0, stats.Hinted)
	require.Equal(t, 3, stats.Errors)
	require.Empty(t, src.saved)
}

func TestRunnerSaveErrorIsCountedNotFatal(t *testing.T) {
	src := &stubSource{pending: clusters("c1"), saveErr: errors.New("db down")}
	sum := stubSummarizer{hints: map[string]Hint{"c1": {ClusterID: "c1", Hint: "h1"}}}
	r := &Runner{Clusters: src, Summarizer: sum}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, stats.Hinted)
	require.Equal(t, 1, stats.Errors)
}

func TestRunnerPropagatesRestaleToSave(t *testing.T) {
	src := &stubSource{pending: clusters("c1")}
	sum := stubSummarizer{hints: map[string]Hint{"c1": {ClusterID: "c1", Hint: "h1"}}}
	r := &Runner{Clusters: src, Summarizer: sum, Restale: true}

	_, err := r.Run(context.Background())
	require.NoError(t, err)
	require.True(t, src.lastRestale, "restale flag must flow through to SaveHint")
}

func TestRunnerNoClustersIsNoOp(t *testing.T) {
	src := &stubSource{pending: nil}
	r := &Runner{Clusters: src, Summarizer: stubSummarizer{}}

	stats, err := r.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, Stats{}, stats)
}
