import { gqlFetch } from '@/lib/graphql';
import { CostSummaryQuery } from '@/lib/queries';
import { formatDollars, formatPercent } from '@/lib/format';

type CostWeek = {
  weekStart: string;
  runs: number;
  spotMinutes: number;
  onDemandMinutes: number;
  totalCost: number;
  costPerBuild: number;
  spotShare: number;
};

async function fetchCost(): Promise<CostWeek[]> {
  const data = await gqlFetch<{ costSummary: CostWeek[] }>(CostSummaryQuery, { weeks: 8 });
  return data?.costSummary ?? [];
}

export default async function CostPage() {
  const weeks = await fetchCost();

  // Bar widths are normalized against the largest costPerBuild in the window —
  // an empty / single-week dataset still renders meaningfully without
  // artifacts.
  const maxPerBuild = weeks.reduce((m, w) => Math.max(m, w.costPerBuild), 0);
  const barWidth = (cpb: number) => (maxPerBuild > 0 ? (cpb / maxPerBuild) * 100 : 0);

  return (
    <div>
      <h1 className="text-2xl font-semibold">Cost (FR-709)</h1>
      <p className="mt-1 text-sm text-gray-600">
        Weekly $/build trend and spot-vs-on-demand share. Pricing is configurable per
        deployment via <code>TEO_COST_SPOT_PER_MIN</code> and{' '}
        <code>TEO_COST_ONDEMAND_PER_MIN</code> (defaults: $0.012 / $0.040 per worker-minute).
      </p>

      <table className="mt-4 w-full text-sm">
        <thead className="border-b text-left">
          <tr>
            <th className="py-2">Week of</th>
            <th>Runs</th>
            <th>$/build</th>
            <th>Spot %</th>
            <th>Total</th>
            <th className="w-1/3">Trend</th>
          </tr>
        </thead>
        <tbody>
          {weeks.length === 0 ? (
            <tr>
              <td colSpan={6} className="py-6 text-center text-gray-500">
                No completed runs in the last 8 weeks.
              </td>
            </tr>
          ) : (
            weeks.map((w) => (
              <tr key={w.weekStart} className="border-b align-middle">
                <td className="py-2 font-mono text-xs">{w.weekStart}</td>
                <td>{w.runs}</td>
                <td>{formatDollars(w.costPerBuild)}</td>
                <td>{formatPercent(w.spotShare)}</td>
                <td>{formatDollars(w.totalCost)}</td>
                <td>
                  <div
                    aria-label={`cost-per-build bar for ${w.weekStart}`}
                    className="h-3 rounded bg-blue-500"
                    style={{ width: `${barWidth(w.costPerBuild)}%` }}
                  />
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
