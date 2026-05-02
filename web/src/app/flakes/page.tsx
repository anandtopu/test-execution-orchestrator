import { gqlFetch } from '@/lib/graphql';
import { FlakesQuery } from '@/lib/queries';
import { formatPercent } from '@/lib/format';

type FlakeRow = {
  testId: string;
  testPath: string;
  testName: string;
  flakeRate: number;
  wilsonLower: number;
  sampleSize: number;
  category: string | null;
};

async function fetchFlakes(): Promise<FlakeRow[]> {
  const data = await gqlFetch<{ flakes: FlakeRow[] }>(FlakesQuery);
  return data?.flakes ?? [];
}

export default async function FlakesPage() {
  const flakes = await fetchFlakes();
  return (
    <div>
      <h1 className="text-2xl font-semibold">Flaky tests</h1>
      <p className="mt-1 text-sm text-gray-600">
        Tests with Wilson 95% lower-bound failure rate above 5%, classified per ADR-0011.
      </p>
      <table className="mt-4 w-full text-sm">
        <thead className="border-b text-left">
          <tr>
            <th>Test</th>
            <th>Rate</th>
            <th>Wilson lo.</th>
            <th>Samples</th>
            <th>Category</th>
          </tr>
        </thead>
        <tbody>
          {flakes.length === 0 ? (
            <tr>
              <td colSpan={5} className="py-6 text-center text-gray-500">
                No flakes detected.
              </td>
            </tr>
          ) : (
            flakes.map((f) => (
              <tr key={f.testId} className="border-b">
                <td className="py-2 font-mono text-xs">
                  {f.testPath}::{f.testName}
                </td>
                <td>{formatPercent(f.flakeRate)}</td>
                <td>{formatPercent(f.wilsonLower)}</td>
                <td>{f.sampleSize}</td>
                <td>{f.category ?? '—'}</td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
