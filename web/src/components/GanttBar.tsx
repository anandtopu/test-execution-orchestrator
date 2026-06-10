import { ganttWidthPct, formatDurationSec } from '@/lib/format';

export interface GanttBarProps {
  index: number;
  status: string;
  /** Headline duration: the actual bar length (or predicted when running). */
  durationMs: number;
  maxMs: number;
  testCount: number;
  // ui-home-calibration: predicted/actual/confidence make this a genuine
  // side-by-side predicted-vs-observed view instead of a single `actual ??
  // predicted` bar. All optional so existing callers keep their single-bar look.
  predictedDurationMs?: number | null;
  actualDurationMs?: number | null;
  predictionConfidence?: number | null;
}

export function GanttBar({
  index,
  status,
  durationMs,
  maxMs,
  testCount,
  predictedDurationMs,
  actualDurationMs,
  predictionConfidence,
}: GanttBarProps) {
  const pct = ganttWidthPct(durationMs, maxMs);
  // Two-layer calibration view: a faint predicted band behind the actual bar.
  const hasPredBand = predictedDurationMs != null && predictedDurationMs > 0;
  const predPct = hasPredBand ? ganttWidthPct(predictedDurationMs as number, maxMs) : 0;
  const showActual = actualDurationMs != null && actualDurationMs > 0;
  const actualPct = showActual ? ganttWidthPct(actualDurationMs as number, maxMs) : pct;
  return (
    <div className="flex items-center gap-2 text-sm" data-testid={`gantt-row-${index}`}>
      <div className="w-16 text-xs">#{index}</div>
      <div className="flex-1 h-5 rounded bg-gray-100 relative">
        {hasPredBand && (
          <div
            className="absolute inset-y-0 left-0 rounded border border-dashed border-blue-300 bg-blue-100/40"
            style={{ width: `${predPct}%` }}
            data-testid={`gantt-pred-${index}`}
          />
        )}
        <div
          className="h-full rounded bg-blue-500 relative"
          style={{ width: `${showActual ? actualPct : pct}%` }}
          data-testid={`gantt-bar-${index}`}
        />
      </div>
      <div className="w-40 text-right text-xs">
        {testCount} tests · {formatDurationSec(durationMs)}
        {predictionConfidence != null && (
          <span className="ml-1 text-gray-400" data-testid={`gantt-conf-${index}`}>
            · conf {(predictionConfidence * 100).toFixed(0)}%
          </span>
        )}
      </div>
      <div className="w-20 text-xs">{status}</div>
    </div>
  );
}
