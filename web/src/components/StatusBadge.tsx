import { statusColorClass } from '@/lib/format';

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`rounded px-2 py-0.5 text-xs ${statusColorClass(status)}`} data-testid="status-badge">
      {status}
    </span>
  );
}
