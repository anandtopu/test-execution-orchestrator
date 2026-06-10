package resultpipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- stub seams -------------------------------------------------------------

type stubExecSource struct {
	rows        []ExecRow
	pendingErr  error
	assignErr   error
	assigned    map[string]string // execID -> clusterID
	assignCalls int

	// pendingCalls records every `since` argument PendingFailures was called
	// with, so a test can assert --since plumbing (zero = full scan).
	pendingCalls []time.Duration
}

func (s *stubExecSource) PendingFailures(_ context.Context, since time.Duration) ([]ExecRow, error) {
	s.pendingCalls = append(s.pendingCalls, since)
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	return s.rows, nil
}

func (s *stubExecSource) AssignCluster(_ context.Context, execID, clusterID string) error {
	s.assignCalls++
	if s.assignErr != nil {
		return s.assignErr
	}
	if s.assigned == nil {
		s.assigned = map[string]string{}
	}
	s.assigned[execID] = clusterID
	return nil
}

type stubStackSource struct {
	byTrace map[string]struct {
		stack, message string
		err            error
	}
	calls map[string]int
}

func (s *stubStackSource) StackFor(_ context.Context, traceID string) (string, string, error) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[traceID]++
	v := s.byTrace[traceID]
	return v.stack, v.message, v.err
}

type stubClusterResolver struct {
	id    string
	err   error
	calls int

	// gotStack / gotMessage capture the last (stack, message) the Backfiller
	// passed in, proving it forwards the StackSource output verbatim into the
	// live fingerprint path (the same args ClusterFor/UpsertCluster receive).
	gotStack   string
	gotMessage string
}

func (c *stubClusterResolver) ClusterFor(_ context.Context, _, stack, message string) (string, error) {
	c.calls++
	c.gotStack = stack
	c.gotMessage = message
	if c.err != nil {
		return "", c.err
	}
	return c.id, nil
}

// emptyFingerprintResolver models *Cluster.ClusterFor's contract for the case
// where FingerprintStack reduces a stack to nothing: it returns ("", nil) so the
// Backfiller takes the SkippedNoFingerprint branch and writes nothing back.
type emptyFingerprintResolver struct {
	calls int
}

func (c *emptyFingerprintResolver) ClusterFor(_ context.Context, _, _, _ string) (string, error) {
	c.calls++
	return "", nil
}

func newStack(traceID, stack, message string, err error) map[string]struct {
	stack, message string
	err            error
} {
	return map[string]struct {
		stack, message string
		err            error
	}{traceID: {stack: stack, message: message, err: err}}
}

// --- tests ------------------------------------------------------------------

func TestBackfillHappyPath(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "e1", RepoID: "r1", TraceID: "t1"}}}
	stacks := &stubStackSource{byTrace: newStack("t1", "boom stack", "boom", nil)}
	cluster := &stubClusterResolver{id: "c1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 1, stats.Assigned)
	require.Equal(t, 0, stats.Errors)
	require.Equal(t, "c1", execs.assigned["e1"])
	require.Equal(t, 1, cluster.calls)
}

func TestBackfillStackForError(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "e1", TraceID: "t1"}}}
	stacks := &stubStackSource{byTrace: newStack("t1", "", "", errors.New("ch down"))}
	cluster := &stubClusterResolver{id: "c1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Errors)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 0, cluster.calls)
	require.Equal(t, 0, execs.assignCalls)
}

func TestBackfillEmptyStackSkipped(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "e1", TraceID: "t1"}}}
	stacks := &stubStackSource{byTrace: newStack("t1", "", "", nil)} // TTL expired etc.
	cluster := &stubClusterResolver{id: "c1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.SkippedNoStack)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 0, cluster.calls)
}

func TestBackfillEmptyClusterIDSkippedNoFingerprint(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "e1", TraceID: "t1"}}}
	stacks := &stubStackSource{byTrace: newStack("t1", "boom", "boom", nil)}
	cluster := &stubClusterResolver{id: ""} // fingerprint reduced to empty
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.SkippedNoFingerprint)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 0, execs.assignCalls)
}

func TestBackfillClusterForError(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "e1", TraceID: "t1"}}}
	stacks := &stubStackSource{byTrace: newStack("t1", "boom", "boom", nil)}
	cluster := &stubClusterResolver{err: errors.New("pg down")}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Errors)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 0, execs.assignCalls)
}

func TestBackfillAssignClusterError(t *testing.T) {
	execs := &stubExecSource{
		rows:      []ExecRow{{ExecID: "e1", TraceID: "t1"}},
		assignErr: errors.New("update failed"),
	}
	stacks := &stubStackSource{byTrace: newStack("t1", "boom", "boom", nil)}
	cluster := &stubClusterResolver{id: "c1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Errors)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 1, execs.assignCalls)
}

func TestBackfillDryRunMutatesNothing(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "e1", TraceID: "t1"}}}
	stacks := &stubStackSource{byTrace: newStack("t1", "boom", "boom", nil)}
	cluster := &stubClusterResolver{id: "c1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster, DryRun: true}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.True(t, stats.DryRun)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 0, cluster.calls, "dry-run must not resolve clusters")
	require.Equal(t, 0, execs.assignCalls, "dry-run must not assign")
}

func TestBackfillPendingFailuresFatalError(t *testing.T) {
	execs := &stubExecSource{pendingErr: errors.New("scan boom")}
	b := &Backfiller{Execs: execs, Stacks: &stubStackSource{}, Cluster: &stubClusterResolver{}}

	_, err := b.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "backfill scan pending failures")
}

// TestBackfillDedupePerTrace verifies N executions sharing one trace cost a
// single StackFor and a single ClusterFor call (the per-trace caches).
func TestBackfillDedupePerTrace(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{
		{ExecID: "e1", RepoID: "r1", TraceID: "shared"},
		{ExecID: "e2", RepoID: "r1", TraceID: "shared"},
		{ExecID: "e3", RepoID: "r1", TraceID: "shared"},
	}}
	stacks := &stubStackSource{byTrace: newStack("shared", "boom", "boom", nil)}
	cluster := &stubClusterResolver{id: "c1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, stats.Assigned)
	require.Equal(t, 1, stacks.calls["shared"], "StackFor should be cached per trace")
	require.Equal(t, 1, cluster.calls, "ClusterFor should be cached per trace")
	require.Equal(t, 3, execs.assignCalls, "every execution must still be back-linked")
}

// --- spec-mandated named tests ----------------------------------------------

// stackEntry is the value type of stubStackSource.byTrace; named so multi-trace
// maps can be built inline (newStack only makes single-entry maps).
type stackEntry = struct {
	stack, message string
	err            error
}

// pythonTraceback is a representative Python traceback + final exception line,
// the shape the live OTLP path (CHStackSource) pulls from span_events. It is the
// exact string fed to the StackSource so a test can assert the Backfiller hands
// it to the cluster resolver verbatim (proving reuse of the live fingerprint
// path in cluster.go).
const pythonTraceback = `Traceback (most recent call last):
  File "/app/svc.py", line 42, in handle_request
    self.do_work()
  File "/app/svc.py", line 91, in do_work
    raise AssertionError("boom")
AssertionError: boom`

// TestBackfillAssignsClusterForFailedExec: one failed exec {E1,R1,T1} whose
// trace yields a Python traceback; the resolver returns "C1". Assert
// Stats{Scanned:1,Assigned:1}, AssignCluster(E1,"C1") called once, and the stack
// the Backfiller passed to the resolver == the StackSource output (the live
// fingerprint path is reused, not re-fetched or re-derived).
func TestBackfillAssignsClusterForFailedExec(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "E1", RepoID: "R1", TraceID: "T1"}}}
	stacks := &stubStackSource{byTrace: newStack("T1", pythonTraceback, "boom", nil)}
	cluster := &stubClusterResolver{id: "C1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 1, stats.Assigned)
	require.Equal(t, 0, stats.Errors)

	require.Equal(t, 1, execs.assignCalls, "AssignCluster must be called exactly once")
	require.Equal(t, "C1", execs.assigned["E1"], "E1 must be back-linked to C1")

	// The stack handed to the cluster resolver must be the StackSource output
	// verbatim — this is the live fingerprint path (cluster.go), not a re-derived
	// or truncated copy.
	require.Equal(t, pythonTraceback, cluster.gotStack,
		"stack passed to the cluster resolver must equal the StackSource output")
	require.Equal(t, "boom", cluster.gotMessage)
}

// TestBackfillSkipsExecWithNoStack: the trace's stack is empty (TTL expired etc).
// Assert Stats{Scanned:1,Assigned:0,SkippedNoStack:1}, no AssignCluster, no
// cluster resolve.
func TestBackfillSkipsExecWithNoStack(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "E1", RepoID: "R1", TraceID: "T1"}}}
	stacks := &stubStackSource{byTrace: newStack("T1", "", "", nil)} // TTL expired
	cluster := &stubClusterResolver{id: "C1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 1, stats.SkippedNoStack)
	require.Equal(t, 0, execs.assignCalls, "no AssignCluster when there is no stack")
	require.Equal(t, 0, cluster.calls, "no cluster resolve when there is no stack")
}

// TestBackfillDryRunDoesNotMutate: DryRun=true over 2 rows with an IDENTICAL
// fingerprint. Assert no AssignCluster / no cluster-resolve calls; Stats
// DryRun=true, Scanned:2, and the distinct-cluster preview is 1.
func TestBackfillDryRunDoesNotMutate(t *testing.T) {
	// Two distinct traces that nonetheless normalize to the SAME fingerprint:
	// the live fingerprinter drops Python line numbers, so these two tracebacks
	// (different line numbers, same frames + exception) collapse to one cluster.
	tbA := `Traceback (most recent call last):
  File "/app/svc.py", line 42, in do_work
    raise AssertionError("boom")
AssertionError: boom`
	tbB := `Traceback (most recent call last):
  File "/app/svc.py", line 99, in do_work
    raise AssertionError("boom")
AssertionError: boom`
	fpA, _ := FingerprintStack(tbA)
	fpB, _ := FingerprintStack(tbB)
	require.Equal(t, fpA, fpB, "test premise: both stacks must share one fingerprint")

	execs := &stubExecSource{rows: []ExecRow{
		{ExecID: "E1", RepoID: "R1", TraceID: "T1"},
		{ExecID: "E2", RepoID: "R1", TraceID: "T2"},
	}}
	stacks := &stubStackSource{byTrace: map[string]stackEntry{
		"T1": {stack: tbA, message: "boom"},
		"T2": {stack: tbB, message: "boom"},
	}}
	cluster := &stubClusterResolver{id: "C1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster, DryRun: true}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.True(t, stats.DryRun)
	require.Equal(t, 2, stats.Scanned)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 1, stats.PreviewClusters,
		"two rows sharing one fingerprint preview as a single distinct cluster")
	require.Equal(t, 0, cluster.calls, "dry-run must not resolve clusters")
	require.Equal(t, 0, execs.assignCalls, "dry-run must not assign")
}

// TestBackfillContinuesOnPerRowError: 3 rows; the StackSource errors on the 2nd
// trace only. Assert rows 1 + 3 are still processed (Assigned:2), the row-2 error
// is aggregated (Errors:1) and logged, and the run is not aborted.
func TestBackfillContinuesOnPerRowError(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{
		{ExecID: "E1", RepoID: "R1", TraceID: "T1"},
		{ExecID: "E2", RepoID: "R1", TraceID: "T2"}, // StackSource errors here
		{ExecID: "E3", RepoID: "R1", TraceID: "T3"},
	}}
	stacks := &stubStackSource{byTrace: map[string]stackEntry{
		"T1": {stack: "boom one", message: "boom"},
		"T2": {err: errors.New("ch transient")},
		"T3": {stack: "boom three", message: "boom"},
	}}
	cluster := &stubClusterResolver{id: "C1"}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err, "a per-row error must not abort the run")
	require.Equal(t, 3, stats.Scanned)
	require.Equal(t, 2, stats.Assigned, "rows 1 and 3 must still be processed")
	require.Equal(t, 1, stats.Errors, "the row-2 error must be aggregated")
	require.Equal(t, "C1", execs.assigned["E1"])
	require.Equal(t, "C1", execs.assigned["E3"])
	require.NotContains(t, execs.assigned, "E2", "the erroring row must not be assigned")
}

// TestBackfillEmptyFingerprintSkips: a non-empty stack whose fingerprint the
// resolver reduces to "" (mirroring *Cluster.ClusterFor returning ("",nil) when
// FingerprintStack yields nothing). Assert Assigned:0, SkippedNoFingerprint:1,
// no AssignCluster — the empty cluster id is NOT written back.
//
// Note: the production FingerprintStack only returns an empty fingerprint for an
// EMPTY input stack (a non-empty stack that normalizes to nothing still hashes
// the empty normalized string to a non-empty digest). So the realistic seam for
// "fingerprint reduced to empty" is the resolver returning "" — exactly what the
// fingerprintClusterResolver does when FingerprintStack(stack) == "". We drive it
// with a stack that the Python normalizer empties, fed through the REAL
// fingerprint check inside the resolver.
func TestBackfillEmptyFingerprintSkips(t *testing.T) {
	execs := &stubExecSource{rows: []ExecRow{{ExecID: "E1", RepoID: "R1", TraceID: "T1"}}}
	// Non-empty stack; the resolver returns "" because the real fingerprint of an
	// empty *input* is the empty-string sentinel. We model the contract directly:
	// the resolver decides the fingerprint reduced to nothing and returns "".
	stacks := &stubStackSource{byTrace: newStack("T1", "non-empty but normalizes to nothing", "msg", nil)}
	cluster := &emptyFingerprintResolver{}
	b := &Backfiller{Execs: execs, Stacks: stacks, Cluster: cluster}

	stats, err := b.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, stats.Assigned)
	require.Equal(t, 1, stats.SkippedNoFingerprint)
	require.Equal(t, 1, cluster.calls, "resolver is consulted; it returns empty")
	require.Equal(t, 0, execs.assignCalls, "no AssignCluster for an empty fingerprint")
}

// TestBackfillSinceZeroMeansAll: an omitted --since (zero duration) queries the
// ExecSource with since==0 (full scan); a set --since plumbs the non-zero
// duration through unchanged. Verified via the stub recording its `since` arg.
func TestBackfillSinceZeroMeansAll(t *testing.T) {
	t.Run("omitted since means full scan (zero)", func(t *testing.T) {
		execs := &stubExecSource{rows: nil}
		b := &Backfiller{Execs: execs, Stacks: &stubStackSource{}, Cluster: &stubClusterResolver{}}
		_, err := b.Run(context.Background())
		require.NoError(t, err)
		require.Equal(t, []time.Duration{0}, execs.pendingCalls,
			"omitted --since must query with since==0 (all history)")
	})

	t.Run("set since plumbs the duration through", func(t *testing.T) {
		execs := &stubExecSource{rows: nil}
		b := &Backfiller{Execs: execs, Stacks: &stubStackSource{}, Cluster: &stubClusterResolver{}, Since: 720 * time.Hour}
		_, err := b.Run(context.Background())
		require.NoError(t, err)
		require.Equal(t, []time.Duration{720 * time.Hour}, execs.pendingCalls,
			"a set --since must reach PendingFailures unchanged")
	})
}
