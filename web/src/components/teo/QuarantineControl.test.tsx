import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QuarantineControl } from './QuarantineControl';

const refreshSpy = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ refresh: refreshSpy }),
}));

describe('<QuarantineControl />', () => {
  afterEach(() => {
    cleanup();
    refreshSpy.mockReset();
  });

  it('renders a Quarantine button for an active test', () => {
    render(<QuarantineControl testId="t-1" quarantined={false} doToggle={vi.fn()} />);
    expect(screen.getByTestId('quarantine-button')).toHaveTextContent('Quarantine');
    expect(screen.queryByTestId('unquarantine-button')).toBeNull();
  });

  it('renders an Unquarantine button for a quarantined test', () => {
    render(<QuarantineControl testId="t-1" quarantined doToggle={vi.fn()} />);
    expect(screen.getByTestId('unquarantine-button')).toHaveTextContent('Unquarantine');
    expect(screen.queryByTestId('quarantine-button')).toBeNull();
  });

  it('confirms a reason then quarantines and refreshes on success', async () => {
    const doToggle = vi.fn().mockResolvedValue({ id: 't-1', status: 'quarantined' });
    render(<QuarantineControl testId="t-1" quarantined={false} doToggle={doToggle} />);

    fireEvent.click(screen.getByTestId('quarantine-button')); // open confirm
    fireEvent.change(screen.getByLabelText('quarantine reason'), { target: { value: 'flaky on CI' } });
    fireEvent.click(screen.getByTestId('quarantine-confirm-button'));

    await waitFor(() => expect(refreshSpy).toHaveBeenCalled());
    expect(doToggle).toHaveBeenCalledWith({ testId: 't-1', quarantine: true, reason: 'flaky on CI' });
  });

  it('sends an undefined reason when the field is left blank', async () => {
    const doToggle = vi.fn().mockResolvedValue({ id: 't-1', status: 'quarantined' });
    render(<QuarantineControl testId="t-1" quarantined={false} doToggle={doToggle} />);
    fireEvent.click(screen.getByTestId('quarantine-button'));
    fireEvent.click(screen.getByTestId('quarantine-confirm-button'));
    await waitFor(() => expect(refreshSpy).toHaveBeenCalled());
    expect(doToggle).toHaveBeenCalledWith({ testId: 't-1', quarantine: true, reason: undefined });
  });

  it('unquarantines directly and refreshes', async () => {
    const doToggle = vi.fn().mockResolvedValue({ id: 't-1', status: 'active' });
    render(<QuarantineControl testId="t-1" quarantined doToggle={doToggle} />);
    fireEvent.click(screen.getByTestId('unquarantine-button'));
    await waitFor(() => expect(refreshSpy).toHaveBeenCalled());
    expect(doToggle).toHaveBeenCalledWith({ testId: 't-1', quarantine: false, reason: undefined });
  });

  it('shows an inline error and does not refresh on failure', async () => {
    const doToggle = vi.fn().mockResolvedValue(null);
    render(<QuarantineControl testId="t-1" quarantined={false} doToggle={doToggle} />);
    fireEvent.click(screen.getByTestId('quarantine-button'));
    fireEvent.click(screen.getByTestId('quarantine-confirm-button'));
    await waitFor(() => expect(screen.getByText('Quarantine failed')).toBeInTheDocument());
    expect(refreshSpy).not.toHaveBeenCalled();
  });

  it('cancels the confirm without calling the toggle', () => {
    const doToggle = vi.fn();
    render(<QuarantineControl testId="t-1" quarantined={false} doToggle={doToggle} />);
    fireEvent.click(screen.getByTestId('quarantine-button'));
    fireEvent.click(screen.getByText('Cancel'));
    expect(screen.getByTestId('quarantine-button')).toBeInTheDocument();
    expect(doToggle).not.toHaveBeenCalled();
  });
});
