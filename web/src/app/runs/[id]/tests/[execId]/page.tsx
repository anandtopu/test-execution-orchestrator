import Link from 'next/link';
import { LogTail } from '@/components/LogTail';

// Test-execution detail page. For v1.0 its primary job is the captured-log
// viewer (S-09-03 / FR-703-704); sparkline + OTel embed remain follow-ups.
export default async function TestExecutionPage({
  params,
}: {
  params: Promise<{ id: string; execId: string }>;
}) {
  const { id, execId } = await params;

  return (
    <div>
      <Link href={`/runs/${id}`} className="text-sm text-blue-600 hover:underline">
        ← Back to run {id.slice(0, 8)}
      </Link>
      <h1 className="mt-2 text-2xl font-semibold">Test execution</h1>
      <dl className="mt-3 grid grid-cols-2 gap-y-1 text-sm sm:grid-cols-4">
        <dt className="text-gray-500">Run</dt>
        <dd className="font-mono">{id.slice(0, 8)}</dd>
        <dt className="text-gray-500">Execution</dt>
        <dd className="font-mono">{execId.slice(0, 8)}</dd>
      </dl>

      <div className="mt-6">
        <LogTail runId={id} execId={execId} />
      </div>
    </div>
  );
}
