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
  fps: [
    { at: '2026-07-11T11:00:00Z', fps: 60, frame_time: 16.6, players: 2 },
    { at: '2026-07-11T11:05:00Z', fps: 58, frame_time: 17.2, players: 3 },
  ],
  player_ping_rank: [
    { user_id: 'u2', name: 'Bo', at: '2026-07-11T11:05:00Z', ping: 120 },
    { user_id: 'u1', name: 'Anu', at: '2026-07-11T11:05:00Z', ping: 40 },
  ],
  user_id: '',
  player_name: '',
  player_latency: [] as { at: string; ping: number }[],
  note: 'FPS from server_metric_samples; latency is per-player poll samples (no IP).',
};
const players = [
  { user_id: 'u1', name: 'Anu', account_name: '', player_id: 'p1' },
  { user_id: 'u2', name: 'Bo', account_name: '', player_id: 'p2' },
] as never[];

beforeEach(() => {
  vi.mocked(getAnalyticsSummary).mockImplementation(async (period) => ({ ...summary, ranking_period: period }));
  vi.mocked(getAnalyticsActivity).mockImplementation(async (requestedRange) => ({ ...activity, range: requestedRange }));
  vi.mocked(getAnalyticsHealth).mockImplementation(async (requestedRange = '24h', userID) => ({
    ...health,
    range: requestedRange,
    user_id: userID ?? '',
    player_name: userID === 'u1' ? 'Anu' : userID === 'u2' ? 'Bo' : '',
    player_latency: userID === 'u2'
      ? [
        { at: '2026-07-11T11:00:00Z', ping: 100 },
        { at: '2026-07-11T11:05:00Z', ping: 120 },
      ]
      : userID === 'u1'
        ? [{ at: '2026-07-11T11:05:00Z', ping: 40 }]
        : [],
  }));
});

describe('AnalyticsDashboard', () => {
  it('loads datasets and shows FPS with visible value stats plus per-player latency ranking', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    expect(screen.getByText('正在加载分析')).toBeInTheDocument();
    expect(await screen.findByText('1h 30m')).toBeInTheDocument();
    expect(getAnalyticsHealth).toHaveBeenCalledWith('24h', undefined, expect.any(AbortSignal));
    expect(await screen.findByRole('img', { name: '服务器 FPS' })).toBeInTheDocument();
    expect(screen.getByText('最新 FPS').parentElement).toHaveTextContent('58');
    expect(screen.getByRole('table', { name: /玩家最近延迟/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Bo' })).toBeInTheDocument();
    // Chart stats always visible (not only after expanding the data table).
    expect(screen.getAllByText('最新').length).toBeGreaterThan(0);
    expect(screen.getByText('选择玩家查看延迟曲线')).toBeInTheDocument();
  });

  it('loads a selected player latency series when picking from the rank table', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByRole('button', { name: 'Bo' });
    fireEvent.click(screen.getByRole('button', { name: 'Bo' }));
    await waitFor(() => expect(getAnalyticsHealth).toHaveBeenCalledWith('24h', 'u2', expect.any(AbortSignal)));
    expect(await screen.findByRole('img', { name: 'Bo 延迟' })).toBeInTheDocument();
    expect(screen.getByText('选中玩家延迟').parentElement).toHaveTextContent('120 ms');
  });

  it('refetches only the endpoint controlled by each filter and exposes pressed state', async () => {
    render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: '本周' }));
    await waitFor(() => expect(getAnalyticsSummary).toHaveBeenCalledWith('week', expect.any(AbortSignal)));
    expect(getAnalyticsActivity).not.toHaveBeenCalled();
    expect(getAnalyticsHealth).not.toHaveBeenCalled();
    vi.clearAllMocks();
    fireEvent.click(screen.getByRole('button', { name: '6 小时' }));
    await waitFor(() => expect(getAnalyticsHealth).toHaveBeenCalledWith('6h', undefined, expect.any(AbortSignal)));
    expect(getAnalyticsSummary).not.toHaveBeenCalled();
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
    expect(screen.getByRole('img', { name: '服务器并发' })).toBeInTheDocument();
  });

  it('shows empty health states without aggregate latency charts', async () => {
    vi.mocked(getAnalyticsSummary).mockResolvedValue({ ...summary, as_of: null, ranking: [], today_observed_ms: 0, peak_count: 0, peak_at: null, active_players: 0 });
    vi.mocked(getAnalyticsActivity).mockResolvedValue({ ...activity, concurrency: [] });
    vi.mocked(getAnalyticsHealth).mockResolvedValue({
      ...health,
      latest_fps: null,
      latest_players: null,
      fps: [],
      player_ping_rank: [],
      player_latency: [],
    });
    render(<AnalyticsDashboard players={[]} refreshKey={0} />);
    expect(await screen.findByText(/没有 FPS 采样/)).toBeInTheDocument();
    expect(screen.getByText(/没有玩家延迟采样/)).toBeInTheDocument();
    expect(screen.queryByRole('img', { name: '延迟 P50' })).not.toBeInTheDocument();
  });

  it('refreshes health with the shared refresh token', async () => {
    const { rerender } = render(<AnalyticsDashboard players={players} refreshKey={0} />);
    await screen.findByText('1h 30m');
    vi.clearAllMocks();
    rerender(<AnalyticsDashboard players={players} refreshKey={1} />);
    await waitFor(() => expect(getAnalyticsHealth).toHaveBeenCalledTimes(1));
    expect(getAnalyticsSummary).toHaveBeenCalledTimes(1);
    expect(getAnalyticsActivity).toHaveBeenCalledTimes(1);
  });
});
