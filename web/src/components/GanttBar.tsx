import { ganttWidthPct, formatDurationSec } from '@/lib/format';

export interface GanttBarProps {
  index: number;
  status: string;
  durationMs: number;
  maxMs: number;
  testCount: number;
}

export function GanttBar({ index, status, durationMs, maxMs, testCount }: GanttBarProps) {
  const pct = ganttWidthPct(durationMs, maxMs);
  return (
    <div className="flex items-center gap-2 text-sm" data-testid={`gantt-row-${index}`}>
      <div className="w-16 text-xs">#{index}</div>
      <div className="flex-1 h-5 rounded bg-gray-100 relative">
        <div
          className="h-full rounded bg-blue-500"
          style={{ width: `${pct}%` }}
          data-testid={`gantt-bar-${index}`}
        />
      </div>
      <div className="w-32 text-right text-xs">
        {testCount} tests · {formatDurationSec(durationMs)}
      </div>
      <div className="w-20 text-xs">{status}</div>
    </div>
  );
}
