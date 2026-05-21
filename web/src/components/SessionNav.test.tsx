import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { SessionNav, type Session } from './SessionNav';

afterEach(cleanup);

describe('<SessionNav />', () => {
  it('shows the signed-in user and a sign-out button', async () => {
    const fetcher = vi.fn().mockResolvedValue({
      userId: 'u1',
      email: 'dev@example.com',
      roles: ['engineer'],
    } satisfies Session);

    render(<SessionNav fetcher={fetcher} />);

    await waitFor(() => expect(screen.getByTestId('session-user')).toBeInTheDocument());
    expect(screen.getByTestId('session-user')).toHaveTextContent('dev@example.com');
    expect(screen.getByTestId('signout')).toBeInTheDocument();
  });

  it('shows a sign-in link when not authenticated', async () => {
    const fetcher = vi.fn().mockResolvedValue(null);
    render(<SessionNav fetcher={fetcher} />);
    await waitFor(() => expect(screen.getByTestId('signin-link')).toBeInTheDocument());
    expect(screen.getByTestId('signin-link')).toHaveAttribute('href', '/login');
  });

  it('treats a fetch error as signed-out', async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error('network'));
    render(<SessionNav fetcher={fetcher} />);
    await waitFor(() => expect(screen.getByTestId('signin-link')).toBeInTheDocument());
  });
});
