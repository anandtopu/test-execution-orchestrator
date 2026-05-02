import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { GanttBar } from './GanttBar';

describe('<GanttBar />', () => {
  it('renders the shard index, test count, and duration', () => {
    render(<GanttBar index={3} status="succeeded" durationMs={5000} maxMs={10000} testCount={42} />);
    const row = screen.getByTestId('gantt-row-3');
    expect(row).toHaveTextContent('#3');
    expect(row).toHaveTextContent('42 tests');
    expect(row).toHaveTextContent('5s');
    expect(row).toHaveTextContent('succeeded');
  });

  it('sizes the bar in proportion to maxMs', () => {
    render(<GanttBar index={0} status="running" durationMs={2500} maxMs={10000} testCount={1} />);
    const bar = screen.getByTestId('gantt-bar-0') as HTMLElement;
    expect(bar.style.width).toBe('25%');
  });

  it('clamps over-long shards to 100%', () => {
    render(<GanttBar index={1} status="running" durationMs={20000} maxMs={10000} testCount={1} />);
    expect((screen.getByTestId('gantt-bar-1') as HTMLElement).style.width).toBe('100%');
  });

  it('hides the bar (0%) when maxMs is invalid', () => {
    render(<GanttBar index={2} status="pending" durationMs={1000} maxMs={0} testCount={1} />);
    expect((screen.getByTestId('gantt-bar-2') as HTMLElement).style.width).toBe('0%');
  });
});
