import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { LogTail, type LogTailResponse } from './LogTail';

afterEach(cleanup);

describe('<LogTail />', () => {
  it('renders the fetched log text', async () => {
    const fetcher = vi.fn().mockResolvedValue({
      text: 'line one\nline two\n',
      truncated: false,
      totalBytes: 18,
      tailBytes: 65536,
    } satisfies LogTailResponse);

    render(<LogTail runId="r1" execId="e1" fetcher={fetcher} />);

    await waitFor(() => expect(screen.getByTestId('log-content')).toHaveTextContent('line one'));
    expect(screen.getByTestId('log-meta')).toHaveTextContent('full log');
    expect(fetcher).toHaveBeenCalledWith('r1', 'e1', 65536);
    // No "Load earlier" button when the full log is shown.
    expect(screen.queryByTestId('log-load-earlier')).toBeNull();
  });

  it('surfaces the "log storage not configured" error', async () => {
    const fetcher = vi.fn().mockResolvedValue({ error: 'Log storage is not configured on this deployment.', status: 501 });
    render(<LogTail runId="r1" execId="e1" fetcher={fetcher} />);
    await waitFor(() => expect(screen.getByTestId('log-error')).toHaveTextContent('not configured'));
  });

  it('doubles the tail window when "Load earlier" is clicked', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce({ text: 'tail', truncated: true, totalBytes: 1_000_000, tailBytes: 65536 })
      .mockResolvedValueOnce({ text: 'more tail', truncated: true, totalBytes: 1_000_000, tailBytes: 131072 });

    render(<LogTail runId="r1" execId="e1" fetcher={fetcher} />);
    await waitFor(() => expect(screen.getByTestId('log-load-earlier')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('log-load-earlier'));

    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    expect(fetcher).toHaveBeenLastCalledWith('r1', 'e1', 131072);
  });
});
