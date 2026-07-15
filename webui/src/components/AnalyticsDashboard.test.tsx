import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getAnalyticsActivity, getAnalyticsHealth, getAnalyticsSummary } from '../api';
import { AnalyticsDashboard } from './AnalyticsDashboard';

vi.mock('../api', async (load) => {
  const actual = await load<typeof import('../api')>();
  return {
    ...actual,
    getAnalyticsSummary: vi.fn(),
    getAnalyticsActivity: vi.fn(),
    getAnalyticsHealth: vi.fn(),
    getAnalyticsBehavior: vi.fn().mockResolvedValue({
      range: 'today',
      sort: 'traveling',
      start: '2026-07-11T00:00:00Z',
      end: '2026-07-12T00:00:00Z',
      timezone: 'Asia/Shanghai',
      ranking: [],
    }),
  };
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
const health = {
  range: '24h' as const,
  start: '2026-07-10T12:00:00Z',
  end: '2026-07-11T12:00:00Z',
  latest_fps: 58,
  latest_players: 3,
  latest_p50: 40,
  latest_p90: 90,
  fps: [
    { at: '2026-07-11T11:00:00Z', fps: 60, frame_time: 16.6, players: 2 },
    { at: '2026-07-11T11:05:00Z', fps: 58, frame_time: 17.2, players: 3 },
  ],
  latency: [
    { at: '2026-07-11T11:00:00Z', sample_count: 2, missing_count: 0, min: 20, p50: 35, p90: 70, p99: 80, max: 90 },
    { at: '2026-07-11T11:05:00Z', sample_count: 3, missing_count: 0, min: 25, p50: 40, p90: 90, p99: 100, max: 110 },
  ],
  note: 'FPS from server_metric_samples; latency percentiles from each successful player poll.',
};
const players = [
  { user_id: 'u1', name: 'Anu', account_name: '', player_id: 'p1' },
  { user_id: 'u2', name: 'Bo', account_name: '', player_id: 'p2' },
] as never[];

beforeEach(() => {
  vi.mocked(getAnalyticsSummary).mockImplementation(async (period) => ({ ...summary, ranking_period: period }));
  vi.mocked(getAnalyticsActivity).mockImplementation(async (requestedRange) => ({ ...activity, range: requestedRange }));
  vi.mocked(getAnalyticsHealth).mockImplementation(async (requestedRange = '24h') => ({ ...health, range: requestedRange }));
});

describe('AnalyticsDashboard', () => {
  it('loads both datasets in parallel and renders metrics, ranking, and null chart gaps', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    expect(screen.getByText('正在加载分析')).toBeInTheDocument();
    expect(await screen.findByText('1h 30m')).toBeInTheDocument();
    expect(screen.getByText('当前在线').parentElement).toHaveTextContent('2');
    expect(screen.getByText('今日峰值').parentElement).toHaveTextContent('4');
    expect(screen.getByRole('row', { name: /Anu在线 1h 00m/ })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '服务器并发' })).toBeInTheDocument();
    expect(getAnalyticsSummary).toHaveBeenCalledWith('today', expect.any(AbortSignal));
    expect(getAnalyticsActivity).toHaveBeenCalledWith('7d', undefined, expect.any(AbortSignal), true);
    expect(getAnalyticsHealth).toHaveBeenCalledWith('24h', expect.any(AbortSignal));
    expect(await screen.findByRole('img', { name: '服务器 FPS' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '延迟 P50' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '延迟 P90' })).toBeInTheDocument();
    expect(screen.getByText('最新 FPS').parentElement).toHaveTextContent('58');
    expect(screen.getByLabelText('健康快照')).toHaveTextContent('90 ms');
  });

  it('refetches only the endpoint controlled by each filter and exposes pressed state', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: '本周' }));
    await waitFor(() => expect(getAnalyticsSummary).toHaveBeenCalledWith('week', expect.any(AbortSignal)));
    expect(getAnalyticsActivity).not.toHaveBeenCalled();
    expect(getAnalyticsHealth).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '本周' })).toHaveAttribute('aria-pressed', 'true');
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: '30 天' }));
    await waitFor(() => expect(getAnalyticsActivity).toHaveBeenCalledWith('30d', undefined, expect.any(AbortSignal), true));
    expect(getAnalyticsSummary).not.toHaveBeenCalled();
    expect(getAnalyticsHealth).not.toHaveBeenCalled();
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: '6 小时' }));
    await waitFor(() => expect(getAnalyticsHealth).toHaveBeenCalledWith('6h', expect.any(AbortSignal)));
    expect(getAnalyticsSummary).not.toHaveBeenCalled();
    expect(getAnalyticsActivity).not.toHaveBeenCalled();
  });

  it('requests selected player history and renders daily observed duration', async () => {
    vi.mocked(getAnalyticsActivity).mockResolvedValueOnce(activity).mockResolvedValueOnce({
      ...activity, player: { user_id: 'u2', name: 'Bo', daily: [{ date: '2026-07-11', observed_ms: 7_200_000 }] },
    });
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    fireEvent.change(screen.getByRole('combobox', { name: '玩家活动' }), { target: { value: 'u2' } });
    await waitFor(() => expect(getAnalyticsActivity).toHaveBeenLastCalledWith('7d', 'u2', expect.any(AbortSignal), false));
    expect(getAnalyticsActivity).toHaveBeenCalledTimes(1);
    expect(await screen.findByRole('img', { name: 'Bo 每日活动' })).toBeInTheDocument();
  });

  it('retains successful data and reports a scoped alert when refresh fails', async () => {
    const { rerender } = render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.mocked(getAnalyticsSummary).mockRejectedValueOnce(new Error('summary unavailable'));
    vi.mocked(getAnalyticsActivity).mockRejectedValueOnce(new Error('activity unavailable'));
    vi.mocked(getAnalyticsHealth).mockRejectedValueOnce(new Error('health unavailable'));
    rerender(<AnalyticsDashboard players={players} refreshKey={1} />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/summary unavailable.*activity unavailable.*health unavailable/i);
    expect(screen.getByText('1h 30m')).toBeInTheDocument();
    expect(screen.getByRole('row', { name: /Anu在线 1h 00m/ })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '服务器并发' })).toBeInTheDocument();
  });

  it('shows an explicit empty state when analytics has no successful data', async () => {
    vi.mocked(getAnalyticsSummary).mockResolvedValue({ ...summary, as_of: null, ranking: [], today_observed_ms: 0, peak_count: 0, peak_at: null, active_players: 0 });
    vi.mocked(getAnalyticsActivity).mockResolvedValue({ ...activity, concurrency: [] });
    vi.mocked(getAnalyticsHealth).mockResolvedValue({
      ...health,
      latest_fps: null,
      latest_players: null,
      latest_p50: null,
      latest_p90: null,
      fps: [],
      latency: [],
    });
    render(<AnalyticsDashboard players={[]} refreshKey={0} />);
    expect(await screen.findByText('该周期没有排行活动数据。')).toBeInTheDocument();
    expect(screen.getAllByText('--').length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText('该范围内没有并发观测数据。')).toBeInTheDocument();
    expect(screen.getByText('选择已知玩家以查看每日活动。')).toBeInTheDocument();
    expect(screen.getByText(/没有服务器健康采样/)).toBeInTheDocument();
  });

  it('refreshes both datasets when the shared refresh token changes', async () => {
    const { rerender } = render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    rerender(<AnalyticsDashboard players={players} refreshKey={1} />);
    await waitFor(() => expect(getAnalyticsSummary).toHaveBeenCalledTimes(1));
    expect(getAnalyticsActivity).toHaveBeenCalledTimes(1);
    expect(getAnalyticsHealth).toHaveBeenCalledTimes(1);
  });

  it('aborts obsolete filter requests and ignores their late results', async () => {
    let resolveOldSummary!: (value: typeof summary) => void;
    let resolveOldActivity!: (value: typeof activity) => void;
    let resolveOldHealth!: (value: typeof health) => void;
    vi.mocked(getAnalyticsSummary).mockImplementationOnce(() => new Promise((resolve) => { resolveOldSummary = resolve; }));
    vi.mocked(getAnalyticsActivity).mockImplementationOnce(() => new Promise((resolve) => { resolveOldActivity = resolve; }));
    vi.mocked(getAnalyticsHealth).mockImplementationOnce(() => new Promise((resolve) => { resolveOldHealth = resolve; }));
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    const oldSummarySignal = vi.mocked(getAnalyticsSummary).mock.calls[0][1]!;
    const oldActivitySignal = vi.mocked(getAnalyticsActivity).mock.calls[0][2]!;
    const oldHealthSignal = vi.mocked(getAnalyticsHealth).mock.calls[0][1]!;

    vi.mocked(getAnalyticsSummary).mockResolvedValueOnce({ ...summary, ranking_period: 'week', ranking: [{ user_id: 'u2', name: 'Latest Bo', observed_ms: 9_000_000, online: false }] });
    vi.mocked(getAnalyticsActivity)
      .mockResolvedValueOnce({ ...activity, range: '30d', concurrency: [{ at: 'latest', average_count: 8, max_count: 8, coverage: 1 }] })
      .mockResolvedValueOnce({ ...activity, range: '30d', concurrency: [{ at: 'selected', average_count: 9, max_count: 9, coverage: 1 }], player: { user_id: 'u2', name: 'Bo', daily: [] } });
    vi.mocked(getAnalyticsHealth).mockResolvedValueOnce({
      ...health,
      range: '6h',
      latest_fps: 30,
      fps: [{ at: 'latest-health', fps: 30, frame_time: 33, players: 1 }],
      latency: [],
    });
    fireEvent.click(screen.getByRole('button', { name: '本周' }));
    fireEvent.click(screen.getByRole('button', { name: '30 天' }));
    fireEvent.click(screen.getByRole('button', { name: '6 小时' }));
    expect(oldSummarySignal.aborted).toBe(true);
    expect(oldActivitySignal.aborted).toBe(true);
    expect(oldHealthSignal.aborted).toBe(true);
    expect(await screen.findByText('Latest Bo')).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', { name: '玩家活动' }), { target: { value: 'u2' } });
    await waitFor(() => expect(getAnalyticsActivity).toHaveBeenLastCalledWith('30d', 'u2', expect.any(AbortSignal), false));
    expect(await screen.findByRole('img', { name: '服务器 FPS' })).toBeInTheDocument();
    expect(screen.getByText('最新 FPS').parentElement).toHaveTextContent('30');

    resolveOldSummary({ ...summary, ranking: [{ user_id: 'u1', name: 'Obsolete Anu', observed_ms: 1, online: false }] });
    resolveOldActivity({ ...activity, concurrency: [{ at: 'obsolete', average_count: 1, max_count: 1, coverage: 1 }] });
    resolveOldHealth({ ...health, latest_fps: 1, fps: [{ at: 'obsolete-health', fps: 1, frame_time: 1, players: 0 }], latency: [] });
    await Promise.resolve();
    expect(screen.queryByText('Obsolete Anu')).not.toBeInTheDocument();
    // Open the concurrency chart data table (after health charts).
    const dataToggles = screen.getAllByRole('button', { name: '显示数据表' });
    fireEvent.click(dataToggles[dataToggles.length - 1]!);
    expect(screen.getByRole('row', { name: 'latest 8' })).toBeInTheDocument();
    expect(screen.queryByRole('row', { name: 'obsolete 1' })).not.toBeInTheDocument();
    expect(screen.queryByRole('row', { name: 'obsolete-health 1' })).not.toBeInTheDocument();
  });

  it('does not present 7-day concurrency under a failed 30-day filter', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByRole('img', { name: '服务器并发' });
    vi.mocked(getAnalyticsActivity).mockRejectedValueOnce(new Error('30 day unavailable'));
    fireEvent.click(screen.getByRole('button', { name: '30 天' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('30 day unavailable');
    expect(screen.queryByRole('img', { name: '服务器并发' })).not.toBeInTheDocument();
  });

  it('does not show player A history under player B and retains a selected option through search', async () => {
    vi.mocked(getAnalyticsActivity)
      .mockResolvedValueOnce(activity)
      .mockResolvedValueOnce({ ...activity, concurrency: [], player: { user_id: 'u1', name: 'Anu', daily: [{ date: '2026-07-11', observed_ms: 60_000 }] } })
      .mockRejectedValueOnce(new Error('Bo unavailable'));
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    fireEvent.change(screen.getByRole('combobox', { name: '玩家活动' }), { target: { value: 'u1' } });
    expect(await screen.findByRole('img', { name: 'Anu 每日活动' })).toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: '查找玩家' }), { target: { value: 'Bo' } });
    expect(screen.getByRole('option', { name: 'Anu' })).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', { name: '玩家活动' }), { target: { value: 'u2' } });
    expect(await screen.findByRole('alert')).toHaveTextContent('Bo unavailable');
    expect(screen.queryByRole('img', { name: 'Anu 每日活动' })).not.toBeInTheDocument();
  });
});
