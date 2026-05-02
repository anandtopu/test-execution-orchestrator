import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { RerunFailedButton } from './RerunFailedButton';

const pushSpy = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushSpy }),
}));

describe('<RerunFailedButton />', () => {
  afterEach(() => {
    cleanup();
    pushSpy.mockReset();
  });

  it('renders nothing when there are no failures to rerun', () => {
    const { container } = render(
      <RerunFailedButton runId="r-1" failedCount={0} doRerun={vi.fn()} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('shows a singular label for a single failure', () => {
    render(<RerunFailedButton runId="r-1" failedCount={1} doRerun={vi.fn()} />);
    expect(screen.getByTestId('rerun-failed-button')).toHaveTextContent('Rerun 1 failed test');
  });

  it('pluralizes the label for >1 failure', () => {
    render(<RerunFailedButton runId="r-1" failedCount={4} doRerun={vi.fn()} />);
    expect(screen.getByTestId('rerun-failed-button')).toHaveTextContent('Rerun 4 failed tests');
  });

  it('navigates to the new run on success', async () => {
    const doRerun = vi.fn().mockResolvedValue({ id: 'new-run-42' });
    render(<RerunFailedButton runId="r-1" failedCount={2} doRerun={doRerun} />);
    fireEvent.click(screen.getByTestId('rerun-failed-button'));
    await waitFor(() => expect(pushSpy).toHaveBeenCalledWith('/runs/new-run-42'));
    expect(doRerun).toHaveBeenCalledWith('r-1');
  });

  it('shows an inline error and does not navigate on failure', async () => {
    const doRerun = vi.fn().mockResolvedValue(null);
    render(<RerunFailedButton runId="r-1" failedCount={2} doRerun={doRerun} />);
    fireEvent.click(screen.getByTestId('rerun-failed-button'));
    await waitFor(() => expect(screen.getByText('Rerun failed')).toBeInTheDocument());
    expect(pushSpy).not.toHaveBeenCalled();
  });
});
