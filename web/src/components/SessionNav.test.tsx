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

  const signedIn: Session = { userId: 'u1', email: 'dev@example.com', roles: ['engineer'] };

  it('does NOT schedule a refresh when unauthenticated', async () => {
    vi.useFakeTimers();
    try {
      const fetcher = vi.fn().mockResolvedValue(null);
      const refresher = vi.fn().mockResolvedValue(true);
      render(<SessionNav fetcher={fetcher} refresher={refresher} refreshMs={1000} />);
      // Let the fetch promise resolve.
      await vi.advanceTimersByTimeAsync(0);
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
      expect(refresher).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('calls refresher after refreshMs for an authenticated session', async () => {
    vi.useFakeTimers();
    try {
      const fetcher = vi.fn().mockResolvedValue(signedIn);
      const refresher = vi.fn().mockResolvedValue(true);
      render(<SessionNav fetcher={fetcher} refresher={refresher} refreshMs={1000} />);
      // Settle the fetch promise (microtasks) WITHOUT advancing the 1000ms
      // interval, so we can assert the interval hasn't fired yet.
      await vi.advanceTimersByTimeAsync(0);
      expect(refresher).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1000);
      expect(refresher).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('flips to the sign-in link and stops refreshing when refresher resolves false', async () => {
    vi.useFakeTimers();
    try {
      const fetcher = vi.fn().mockResolvedValue(signedIn);
      const refresher = vi.fn().mockResolvedValue(false);
      render(<SessionNav fetcher={fetcher} refresher={refresher} refreshMs={1000} />);
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(1000);
      expect(refresher).toHaveBeenCalledTimes(1);
      vi.useRealTimers();
      await waitFor(() => expect(screen.getByTestId('signin-link')).toBeInTheDocument());
      // Interval was cleared: advancing further fires no more refreshes.
      vi.useFakeTimers();
      await vi.advanceTimersByTimeAsync(5000);
      expect(refresher).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('flips to signed-out and stops refreshing when refresher throws', async () => {
    vi.useFakeTimers();
    try {
      const fetcher = vi.fn().mockResolvedValue(signedIn);
      const refresher = vi.fn().mockRejectedValue(new Error('boom'));
      render(<SessionNav fetcher={fetcher} refresher={refresher} refreshMs={1000} />);
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(1000);
      expect(refresher).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(5000);
      expect(refresher).toHaveBeenCalledTimes(1); // cleared, no more calls
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not schedule a refresh when refreshMs <= 0 (disabled)', async () => {
    vi.useFakeTimers();
    try {
      const fetcher = vi.fn().mockResolvedValue(signedIn);
      const refresher = vi.fn().mockResolvedValue(true);
      render(<SessionNav fetcher={fetcher} refresher={refresher} refreshMs={0} />);
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(60_000);
      expect(refresher).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('clears the interval on unmount (no refresh after teardown)', async () => {
    vi.useFakeTimers();
    try {
      const fetcher = vi.fn().mockResolvedValue(signedIn);
      const refresher = vi.fn().mockResolvedValue(true);
      const { unmount } = render(<SessionNav fetcher={fetcher} refresher={refresher} refreshMs={1000} />);
      await vi.advanceTimersByTimeAsync(0);
      unmount();
      await vi.advanceTimersByTimeAsync(5000);
      expect(refresher).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});
