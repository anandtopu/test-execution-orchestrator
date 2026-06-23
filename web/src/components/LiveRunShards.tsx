'use client';

import { useEffect, useState } from 'react';
import { GanttBar } from './GanttBar';
import { isLive } from '@/lib/format';

export interface Shard {
  id: string;
  index: number;
  status: string;
  workerId?: string | null;
  predictedDurationMs: number;
  actualDurationMs?: number | null;
  testCount: number;
  startedAt?: string | null;
  finishedAt?: string | null;
  // ui-home-calibration: per-shard calibration metadata (null until the sibling
  // migration adds teo.shards.prediction_confidence + model_version) so the
  // Gantt is a true predicted-vs-observed view, not an `actual ?? predicted`
  // fallback.
  predictionConfidence?: number | null;
  modelVersion?: string | null;
}

export interface LiveRun {
  id: string;
  status: string;
  shards: Shard[];
}

/** A subscriber opens a live stream of run snapshots and returns an unsubscribe
 * function. onError is called when the stream can't be established or fails, so
 * the caller can fall back to polling. */
export type RunSubscriber = (
  runId: string,
  onData: (run: LiveRun) => void,
  onError: () => void,
) => () => void;

export interface LiveRunShardsProps {
  /** Initial server-rendered run, used as the first render's source of truth. */
  initial: LiveRun;
  /** How often to refetch when the run is non-terminal. Defaults to 2000 ms. */
  pollMs?: number;
  /** Override fetch impl for tests; defaults to `window.fetch`. */
  fetcher?: (runId: string) => Promise<LiveRun | null>;
  /** Override the live subscription for tests; defaults to a graphql-ws client. */
  subscriber?: RunSubscriber;
}

/** Default fetcher: hits the same Next.js route prefix the page already uses,
 * via the GraphQL endpoint exposed at /api/graphql/run. We piggyback on a
 * tiny Next API route so the browser doesn't need TEO_API_URL or the API key.
 */
async function defaultFetcher(runId: string): Promise<LiveRun | null> {
  const res = await fetch(`/api/graphql/run?id=${encodeURIComponent(runId)}`, {
    cache: 'no-store',
  });
  if (!res.ok) return null;
  return (await res.json()) as LiveRun;
}

const RUN_CHANGED_SUBSCRIPTION = `subscription RunChanged($id: ID!) {
  runChanged(id: $id) {
    id
    status
    shards {
      id index status workerId predictedDurationMs actualDurationMs
      testCount startedAt finishedAt predictionConfidence modelVersion
    }
  }
}`;

/** Same-origin WebSocket endpoint for GraphQL subscriptions. The teo_session
 * cookie rides the upgrade automatically (httpOnly, same-origin), which is the
 * supported auth path — browsers can't set an Authorization header on a WS. */
function wsURL(): string | null {
  const override = process.env.NEXT_PUBLIC_WS_URL;
  if (override) return override;
  if (typeof window === 'undefined') return null;
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}/graphql/subscriptions`;
}

/** Default subscriber: a minimal graphql-transport-ws client over the native
 * WebSocket (no extra dependency — it mirrors the protocol the API server
 * implements). Calls onError (→ poll fallback) when WebSockets are unavailable
 * or the stream errors, matching the repo's NATS-optional philosophy. */
const defaultSubscriber: RunSubscriber = (runId, onData, onError) => {
  const url = wsURL();
  if (!url || typeof WebSocket === 'undefined') {
    onError();
    return () => {};
  }
  let closed = false;
  let completed = false;
  let ws: WebSocket;
  try {
    ws = new WebSocket(url, 'graphql-transport-ws');
  } catch {
    onError();
    return () => {};
  }
  const opID = '1';
  ws.onopen = () => ws.send(JSON.stringify({ type: 'connection_init' }));
  ws.onmessage = (ev) => {
    let msg: { type?: string; payload?: { data?: { runChanged?: LiveRun } } };
    try {
      msg = JSON.parse(ev.data as string);
    } catch {
      return;
    }
    switch (msg.type) {
      case 'connection_ack':
        ws.send(
          JSON.stringify({
            id: opID,
            type: 'subscribe',
            payload: { query: RUN_CHANGED_SUBSCRIPTION, variables: { id: runId } },
          }),
        );
        break;
      case 'next': {
        const next = msg.payload?.data?.runChanged;
        if (next && !closed) onData(next);
        break;
      }
      case 'complete':
        completed = true; // server ended the stream (terminal run) — not an error
        break;
      case 'error':
        if (!closed) onError();
        break;
      case 'ping':
        ws.send(JSON.stringify({ type: 'pong' }));
        break;
    }
  };
  ws.onerror = () => {
    if (!closed) onError();
  };
  ws.onclose = () => {
    if (!closed && !completed) onError();
  };
  return () => {
    closed = true;
    try {
      ws.close();
    } catch {
      /* already closing */
    }
  };
};

/**
 * Renders the shard Gantt and streams live updates while the run is non-terminal.
 * It subscribes over WebSocket (graphql-ws) first and only falls back to polling
 * every `pollMs` if the subscription can't be established or errors. Both stop
 * automatically when the run reaches a terminal status.
 */
export function LiveRunShards({
  initial,
  pollMs = 2000,
  fetcher = defaultFetcher,
  subscriber = defaultSubscriber,
}: LiveRunShardsProps) {
  const [run, setRun] = useState<LiveRun>(initial);
  const [wsFailed, setWsFailed] = useState(false);

  // Preferred path: live WebSocket subscription.
  useEffect(() => {
    if (!isLive(run.status)) return;
    let cancelled = false;
    const unsubscribe = subscriber(
      run.id,
      (next) => {
        if (!cancelled) setRun(next);
      },
      () => {
        if (!cancelled) setWsFailed(true);
      },
    );
    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, [run.id, run.status, subscriber]);

  // Fallback path: poll only when the subscription is unavailable.
  useEffect(() => {
    if (!isLive(run.status)) return;
    if (!wsFailed) return;
    let cancelled = false;
    const id = setInterval(async () => {
      const next = await fetcher(run.id);
      if (cancelled || !next) return;
      setRun(next);
    }, pollMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [run.id, run.status, wsFailed, pollMs, fetcher]);

  const shards = run.shards ?? [];
  // Scale to the longer of predicted/actual across all shards so the predicted
  // band and the actual bar are both visible against a single calibrated axis.
  const maxMs = Math.max(
    ...shards.map((s) => Math.max(s.actualDurationMs ?? 0, s.predictedDurationMs)),
    1,
  );

  if (shards.length === 0) {
    return <p className="text-sm text-gray-500">No shards yet.</p>;
  }
  return (
    <div className="mt-2 space-y-1" data-testid="live-run-shards" data-status={run.status}>
      {shards.map((s) => (
        <GanttBar
          key={s.id}
          index={s.index}
          status={s.status}
          durationMs={s.actualDurationMs ?? s.predictedDurationMs}
          maxMs={maxMs}
          testCount={s.testCount}
          predictedDurationMs={s.predictedDurationMs}
          actualDurationMs={s.actualDurationMs}
          predictionConfidence={s.predictionConfidence}
        />
      ))}
    </div>
  );
}
