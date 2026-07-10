import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getAnalyticsActivity, getAnalyticsSummary } from '../api';
import { AnalyticsDashboard } from './AnalyticsDashboard';

vi.mock('../api', async (load) => {
  const actual = await load<typeof import('../api')>();
  return { ...actual, getAnalyticsSummary: vi.fn(), getAnalyticsActivity: vi.fn() };
});

const summary = {
  online_count: 2, as_of: '2026-07-11T12:00:00Z', today_observed_ms: 5_400_000,
  peak_count: 4, peak_at: '2026-07-11T10:30:00Z', active_players: 3,
  ranking_period: 'today' as const,
  ranking: [
    { user_id: 'u1', name: 'Anu', observed_ms: 3_600_000, online: true },
    { user_id: 'u2', name: 'Bo', observed_ms: 1_800_000, online: false },
  ],
};
const activity = {
  range: '7d' as const, timezone: 'Asia/Shanghai', start: '2026-07-05', end: '2026-07-11',
  concurrency: [
    { at: '2026-07-11T10:00:00Z', average_count: 1.5, max_count: 2, coverage: 1 },
    { at: '2026-07-11T10:05:00Z', average_count: null, max_count: null, coverage: 0 },
  ],
  player: null,
};
const players = [
  { user_id: 'u1', name: 'Anu', account_name: '', player_id: 'p1' },
  { user_id: 'u2', name: 'Bo', account_name: '', player_id: 'p2' },
] as never[];

beforeEach(() => {
  vi.mocked(getAnalyticsSummary).mockResolvedValue(summary);
  vi.mocked(getAnalyticsActivity).mockResolvedValue(activity);
});

describe('AnalyticsDashboard', () => {
  it('loads both datasets in parallel and renders metrics, ranking, and null chart gaps', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    expect(screen.getByText('Loading analytics')).toBeInTheDocument();
    expect(await screen.findByText('1h 30m')).toBeInTheDocument();
    expect(screen.getByText('Online now').parentElement).toHaveTextContent('2');
    expect(screen.getByText('Peak today').parentElement).toHaveTextContent('4');
    expect(screen.getByRole('row', { name: /AnuOnline 1h 00m/ })).toBeInTheDocument();
    expect(screen.getAllByTestId('line-segment')).toHaveLength(1);
    expect(getAnalyticsSummary).toHaveBeenCalledWith('today', expect.any(AbortSignal));
    expect(getAnalyticsActivity).toHaveBeenCalledWith('7d', undefined, expect.any(AbortSignal));
  });

  it('refetches only the endpoint controlled by each filter and exposes pressed state', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: 'Week' }));
    await waitFor(() => expect(getAnalyticsSummary).toHaveBeenCalledWith('week', expect.any(AbortSignal)));
    expect(getAnalyticsActivity).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Week' })).toHaveAttribute('aria-pressed', 'true');
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: '30 days' }));
    await waitFor(() => expect(getAnalyticsActivity).toHaveBeenCalledWith('30d', undefined, expect.any(AbortSignal)));
    expect(getAnalyticsSummary).not.toHaveBeenCalled();
  });

  it('requests selected player history and renders daily observed duration', async () => {
    vi.mocked(getAnalyticsActivity).mockResolvedValueOnce(activity).mockResolvedValueOnce({
      ...activity, player: { user_id: 'u2', name: 'Bo', daily: [{ date: '2026-07-11', observed_ms: 7_200_000 }] },
    });
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    fireEvent.change(screen.getByRole('combobox', { name: 'Player activity' }), { target: { value: 'u2' } });
    await waitFor(() => expect(getAnalyticsActivity).toHaveBeenLastCalledWith('7d', 'u2', expect.any(AbortSignal)));
    expect(await screen.findByRole('img', { name: 'Bo daily activity' })).toBeInTheDocument();
  });

  it('retains successful data and reports a scoped alert when refresh fails', async () => {
    const { rerender } = render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.mocked(getAnalyticsSummary).mockRejectedValueOnce(new Error('summary unavailable'));
    vi.mocked(getAnalyticsActivity).mockRejectedValueOnce(new Error('activity unavailable'));
    rerender(<AnalyticsDashboard players={players} refreshKey={1} />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/summary unavailable.*activity unavailable/i);
    expect(screen.getByText('1h 30m')).toBeInTheDocument();
    expect(screen.getByRole('row', { name: /AnuOnline 1h 00m/ })).toBeInTheDocument();
  });

  it('shows an explicit empty state when analytics has no successful data', async () => {
    vi.mocked(getAnalyticsSummary).mockResolvedValue({ ...summary, as_of: null, ranking: [], today_observed_ms: 0, peak_count: 0, peak_at: null, active_players: 0 });
    vi.mocked(getAnalyticsActivity).mockResolvedValue({ ...activity, concurrency: [] });
    render(<AnalyticsDashboard players={[]} refreshKey={0} />);
    expect(await screen.findByText('No ranking activity for this period.')).toBeInTheDocument();
    expect(screen.getAllByText('--')).toHaveLength(4);
    expect(screen.getByText('No concurrency observations for this range.')).toBeInTheDocument();
    expect(screen.getByText('Select a known player to inspect daily activity.')).toBeInTheDocument();
  });

  it('refreshes both datasets when the shared refresh token changes', async () => {
    const { rerender } = render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    rerender(<AnalyticsDashboard players={players} refreshKey={1} />);
    await waitFor(() => expect(getAnalyticsSummary).toHaveBeenCalledTimes(1));
    expect(getAnalyticsActivity).toHaveBeenCalledTimes(1);
  });
});
