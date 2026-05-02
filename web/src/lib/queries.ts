// Centralised GraphQL operation strings — referenced once from each page so
// schema renames are a single-file change (per E-09 strategy §6).

export const RunsQuery = /* GraphQL */ `
  query Runs($first: Int) {
    runs(first: $first) {
      id
      repoFullName
      branch
      commitSha
      status
      totalDurationMs
      startedAt
      finishedAt
    }
  }
`;

export const RunByIdQuery = /* GraphQL */ `
  query Run($id: ID!) {
    run(id: $id) {
      id
      repoFullName
      branch
      commitSha
      status
      totalDurationMs
      preemptionCount
      startedAt
      finishedAt
      failedTestCount
      shards {
        id
        index
        status
        workerId
        predictedDurationMs
        actualDurationMs
        testCount
        startedAt
        finishedAt
      }
    }
  }
`;

export const FailureClustersQuery = /* GraphQL */ `
  query FailureClusters {
    failureClusters {
      id
      representativeMessage
      representativeStack
      occurrences
      firstSeen
      lastSeen
    }
  }
`;

export const FlakesQuery = /* GraphQL */ `
  query Flakes {
    flakes {
      testId
      testPath
      testName
      flakeRate
      wilsonLower
      sampleSize
      category
    }
  }
`;

export const CostSummaryQuery = /* GraphQL */ `
  query CostSummary($weeks: Int) {
    costSummary(weeks: $weeks) {
      weekStart
      runs
      spotMinutes
      onDemandMinutes
      totalCost
      costPerBuild
      spotShare
    }
  }
`;

export const RerunFailedMutation = /* GraphQL */ `
  mutation RerunFailed($runId: ID!) {
    rerunFailed(runId: $runId) {
      id
    }
  }
`;
