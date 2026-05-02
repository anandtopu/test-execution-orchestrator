import { describe, expect, it } from 'vitest';
import {
  formatDollars,
  formatDurationSec,
  formatPercent,
  ganttWidthPct,
  isLive,
  shortSha,
  statusColorClass,
} from './format';

describe('statusColorClass', () => {
  it('returns green for succeeded', () => {
    expect(statusColorClass('succeeded')).toContain('green');
  });
  it('returns red for failed', () => {
    expect(statusColorClass('failed')).toContain('red');
  });
  it('returns gray for cancelled', () => {
    expect(statusColorClass('cancelled')).toContain('gray');
  });
  it('falls back to blue for unknown / in-flight', () => {
    expect(statusColorClass('running')).toContain('blue');
    expect(statusColorClass('pending')).toContain('blue');
    expect(statusColorClass('whatever')).toContain('blue');
  });
});

describe('ganttWidthPct', () => {
  it('computes proportional width', () => {
    expect(ganttWidthPct(50, 100)).toBe(50);
    expect(ganttWidthPct(25, 100)).toBe(25);
    expect(ganttWidthPct(100, 100)).toBe(100);
  });
  it('clamps to [0, 100]', () => {
    expect(ganttWidthPct(-5, 100)).toBe(0);
    expect(ganttWidthPct(200, 100)).toBe(100);
  });
  it('returns 0 for invalid max', () => {
    expect(ganttWidthPct(50, 0)).toBe(0);
    expect(ganttWidthPct(50, -1)).toBe(0);
  });
});

describe('formatDurationSec', () => {
  it('rounds to whole seconds with s suffix', () => {
    expect(formatDurationSec(1500)).toBe('2s');
    expect(formatDurationSec(900)).toBe('1s');
    expect(formatDurationSec(60_000)).toBe('60s');
  });
  it('returns em-dash for missing or zero', () => {
    expect(formatDurationSec(null)).toBe('—');
    expect(formatDurationSec(undefined)).toBe('—');
    expect(formatDurationSec(0)).toBe('—');
  });
});

describe('formatPercent', () => {
  it('renders one decimal place', () => {
    expect(formatPercent(0.05)).toBe('5.0%');
    expect(formatPercent(0.123)).toBe('12.3%');
    expect(formatPercent(1)).toBe('100.0%');
  });
  it('handles missing values', () => {
    expect(formatPercent(null)).toBe('—');
    expect(formatPercent(undefined)).toBe('—');
  });
});

describe('shortSha', () => {
  it('returns the first 7 chars', () => {
    expect(shortSha('abcdef0123456789')).toBe('abcdef0');
  });
  it('returns empty for missing input', () => {
    expect(shortSha(null)).toBe('');
    expect(shortSha(undefined)).toBe('');
    expect(shortSha('')).toBe('');
  });
});

describe('formatDollars', () => {
  it('renders two-decimal USD amounts', () => {
    expect(formatDollars(0)).toBe('$0.00');
    expect(formatDollars(1.234)).toBe('$1.23');
    expect(formatDollars(99.999)).toBe('$100.00');
  });
  it('handles missing/NaN values', () => {
    expect(formatDollars(null)).toBe('—');
    expect(formatDollars(undefined)).toBe('—');
    expect(formatDollars(Number.NaN)).toBe('—');
  });
});

describe('isLive', () => {
  it('treats terminal statuses as not live', () => {
    expect(isLive('succeeded')).toBe(false);
    expect(isLive('failed')).toBe(false);
    expect(isLive('cancelled')).toBe(false);
  });
  it('treats every other status as live', () => {
    expect(isLive('pending')).toBe(true);
    expect(isLive('planning')).toBe(true);
    expect(isLive('dispatching')).toBe(true);
    expect(isLive('running')).toBe(true);
    expect(isLive('finalizing')).toBe(true);
  });
});
