import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusBadge } from './StatusBadge';

describe('<StatusBadge />', () => {
  it('renders the status text', () => {
    render(<StatusBadge status="succeeded" />);
    expect(screen.getByTestId('status-badge')).toHaveTextContent('succeeded');
  });

  it('applies the green palette for succeeded runs', () => {
    render(<StatusBadge status="succeeded" />);
    const el = screen.getByTestId('status-badge');
    expect(el.className).toMatch(/green/);
  });

  it('applies the red palette for failed runs', () => {
    render(<StatusBadge status="failed" />);
    expect(screen.getByTestId('status-badge').className).toMatch(/red/);
  });

  it('falls back to the in-flight palette for unknown statuses', () => {
    render(<StatusBadge status="planning" />);
    expect(screen.getByTestId('status-badge').className).toMatch(/blue/);
  });
});
